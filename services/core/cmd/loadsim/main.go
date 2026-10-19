// Command loadsim is the cohort simulator (docs/09, load track): a whole
// cohort starts the same exam within one minute and autosaves until it hands
// in, through the same procedures the runner calls. It reports latency per
// procedure and, the one number that must be zero, answers lost: every
// event a candidate had acknowledged is looked for in the attempt's log.
//
// It needs a core started with AM_DEV=1 AM_MAILER=mailbox (it reads the
// links and codes candidates would get by email), AM_TRUST_PROXY=1 (each
// simulated candidate comes from its own address, as in an exam hall behind
// NAT they would not, but the per-address limits are not what is measured),
// AM_DEV_MAILBOX_KEEP at least 3× the cohort, and AM_LIMIT_PER_TENANT above
// 4× it, since the whole cohort is one tenant. scripts/load.sh does that.
//
//	loadsim -url http://localhost:8090 -staff staff@example.id -n 200 -minutes 3
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1/assessmatrixv1connect"
)

var (
	base    = flag.String("url", "http://localhost:8090", "the core")
	staff   = flag.String("staff", "", "a staff email of the tenant to use (core tenant …)")
	n       = flag.Int("n", 100, "candidates in the cohort")
	minutes = flag.Float64("minutes", 3, "how long each candidate works before handing in")
	every   = flag.Duration("save-every", 10*time.Second, "autosave interval while answering")
	ramp    = flag.Duration("ramp", time.Minute, "everyone starts within this window")
	items   = flag.Int("items", 30, "questions in the exam")
)

var (
	codeRe = regexp.MustCompile(`\b(\d{6})\b`)
	linkRe = regexp.MustCompile(`#/i/([A-Za-z0-9_-]{43})`)
)

func main() {
	flag.Parse()
	if *staff == "" {
		fail(errors.New("-staff is required"))
	}
	ctx := context.Background()
	s := newClient("10.255.255.254")
	if err := s.signIn(ctx, *staff); err != nil {
		fail(fmt.Errorf("staff sign-in: %w", err))
	}
	exam, emails, err := s.prepare(ctx)
	if err != nil {
		fail(err)
	}
	fmt.Printf("cohort of %d on exam %s: start within %s, work %.1f min, autosave every %s\n", *n, exam, *ramp, *minutes, *every)

	var wg sync.WaitGroup
	results := make([]*candidate, len(emails))
	start := time.Now()
	for i, email := range emails {
		c := &candidate{client: newClient(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)), email: email}
		results[i] = c
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(rand.Int64N(int64(*ramp))))
			c.err = c.sit(ctx)
		}()
	}
	wg.Wait()
	took := time.Since(start)

	// The one number that must be zero.
	var lost, sent, failed int
	for _, c := range results {
		if c.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  %s: %v\n", c.email, c.err)
			continue
		}
		got, err := s.stored(ctx, c.attempt)
		if err != nil {
			fail(fmt.Errorf("read back %s: %w", c.attempt, err))
		}
		for k, want := range c.acked {
			sent += want
			if got[k] < want {
				lost += want - got[k]
			}
		}
	}
	report(took)
	fmt.Printf("\ncandidates finished: %d/%d · events acknowledged: %d · answers lost: %d\n", len(results)-failed, len(results), sent, lost)
	if lost > 0 || failed > 0 {
		os.Exit(1)
	}
}

// ── one candidate ──────────────────────────────────────────────────────────

type candidate struct {
	*client
	email   string
	attempt string
	// acked counts acknowledged events by kind and item, as the log should hold them.
	acked map[string]int
	err   error
}

func (c *candidate) sit(ctx context.Context) error {
	token, err := waitMail(ctx, c.email, "invite", linkRe)
	if err != nil {
		return err
	}
	if r, err := timed("OpenLink", func() (*connect.Response[v1.OpenLinkResponse], error) {
		return c.candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: token}))
	}); err != nil || !r.Msg.GetOk() {
		return fmt.Errorf("open link: %v %v", err, refusal(r))
	}
	if r, err := timed("RequestCode", func() (*connect.Response[v1.RequestCodeResponse], error) {
		return c.candidate.RequestCode(ctx, connect.NewRequest(&v1.RequestCodeRequest{Token: token}))
	}); err != nil || !r.Msg.GetOk() {
		return fmt.Errorf("request code: %v", err)
	}
	code, err := waitMail(ctx, c.email, "code", codeRe)
	if err != nil {
		return err
	}
	if r, err := timed("VerifyCode", func() (*connect.Response[v1.VerifyCodeResponse], error) {
		return c.candidate.VerifyCode(ctx, connect.NewRequest(&v1.VerifyCodeRequest{Token: token, Code: code}))
	}); err != nil || !r.Msg.GetOk() {
		return fmt.Errorf("verify code: %v", err)
	}
	sess, err := timed("GetSession", func() (*connect.Response[v1.GetSessionResponse], error) {
		return c.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	})
	if err != nil {
		return err
	}
	questions := allItems(sess.Msg.GetExam())
	att, err := timed("StartAttempt", func() (*connect.Response[v1.StartAttemptResponse], error) {
		return c.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{}))
	})
	if err != nil {
		return err
	}
	c.attempt = att.Msg.GetAttempt().GetId()
	c.acked = map[string]int{}

	end := time.Now().Add(time.Duration(*minutes * float64(time.Minute)))
	batch := 0
	for q := 0; ; q++ {
		// Answer a question or two, change a mind now and then; save.
		var evs []*v1.AttemptEvent
		for range 1 + rand.IntN(2) {
			it := questions[rand.IntN(len(questions))]
			ch := it.GetChoices()[rand.IntN(len(it.GetChoices()))].GetId()
			evs = append(evs, &v1.AttemptEvent{Kind: "response.changed", ItemId: it.GetId(), Values: []string{ch}, ClientTime: time.Now().UnixMilli()})
		}
		batch++
		if err := c.save(ctx, fmt.Sprintf("b%d", batch), evs); err != nil {
			return err
		}
		if time.Now().After(end) {
			break
		}
		time.Sleep(*every/2 + time.Duration(rand.Int64N(int64(*every))))
	}
	_, err = timed("SubmitAttempt", func() (*connect.Response[v1.SubmitAttemptResponse], error) {
		return c.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: c.attempt}))
	})
	return err
}

// save sends one batch the way the runner does: the same batch id until the
// server acknowledges it, backing off, for as long as the exam lasts.
func (c *candidate) save(ctx context.Context, id string, evs []*v1.AttemptEvent) error {
	wait := 500 * time.Millisecond
	for try := 0; ; try++ {
		_, err := timed("AppendEvents", func() (*connect.Response[v1.AppendEventsResponse], error) {
			return c.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: c.attempt, BatchId: id, Events: evs}))
		})
		if err == nil {
			for _, e := range evs {
				c.acked[e.GetKind()+"/"+e.GetItemId()]++
			}
			return nil
		}
		switch connect.CodeOf(err) {
		case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeNotFound:
			return fmt.Errorf("save %s refused: %w", id, err)
		}
		if try > 20 {
			return fmt.Errorf("save %s still failing after %d tries: %w", id, try, err)
		}
		time.Sleep(wait)
		wait = min(wait*2, 10*time.Second)
	}
}

// ── staff side ─────────────────────────────────────────────────────────────

func (s *client) signIn(ctx context.Context, email string) error {
	if _, err := s.auth.RequestStaffCode(ctx, connect.NewRequest(&v1.RequestStaffCodeRequest{Email: email})); err != nil {
		return err
	}
	code, err := waitMail(ctx, email, "staff-code", codeRe)
	if err != nil {
		return err
	}
	_, err = s.auth.VerifyStaffCode(ctx, connect.NewRequest(&v1.VerifyStaffCodeRequest{Email: email, Code: code}))
	return err
}

// prepare publishes an exam of single-choice questions and invites the cohort.
func (s *client) prepare(ctx context.Context) (string, []string, error) {
	e, err := s.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_EXAM}))
	if err != nil {
		return "", nil, err
	}
	exam := e.Msg.GetExam()
	exam.Title = fmt.Sprintf("Load %s", time.Now().Format("2006-01-02 15:04:05"))
	exam.Timed = true
	exam.Tier = 0 // integrity signals are not what is measured
	sec := &v1.Section{Id: "s1", Name: "Bagian 1", Minutes: int32(*minutes + ramp.Minutes() + 10)}
	for i := range *items {
		sec.Items = append(sec.Items, &v1.Item{Id: fmt.Sprintf("q%d", i+1), Type: v1.ItemType_ITEM_TYPE_SINGLE,
			Stem: fmt.Sprintf("Soal %d", i+1), Points: 1, Correct: []string{"a"},
			Choices: []*v1.Choice{{Id: "a", Text: "A"}, {Id: "b", Text: "B"}, {Id: "c", Text: "C"}, {Id: "d", Text: "D"}}})
	}
	exam.Sections = []*v1.Section{sec}
	saved, err := s.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: exam, Mode: v1.SaveMode_SAVE_MODE_DRAFT}))
	if err != nil {
		return "", nil, fmt.Errorf("save exam: %w", err)
	}
	id := saved.Msg.GetExam().GetId()
	if _, err := s.exams.SetExamStatus(ctx, connect.NewRequest(&v1.SetExamStatusRequest{Id: id, Status: v1.Status_STATUS_PUBLISHED})); err != nil {
		return "", nil, fmt.Errorf("publish: %w", err)
	}
	run := fmt.Sprintf("%x", time.Now().UnixNano())[8:]
	var emails []string
	for i := range *n {
		emails = append(emails, fmt.Sprintf("load.%s.%05d@example.test", run, i))
	}
	for start := 0; start < len(emails); start += 1000 {
		var drafts []*v1.PersonDraft
		for i, e := range emails[start:min(start+1000, len(emails))] {
			drafts = append(drafts, &v1.PersonDraft{Name: fmt.Sprintf("Peserta %05d", start+i), Email: e})
		}
		ppl, err := s.people.EnsurePeople(ctx, connect.NewRequest(&v1.EnsurePeopleRequest{People: drafts}))
		if err != nil {
			return "", nil, fmt.Errorf("people: %w", err)
		}
		if _, err := s.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: id, PersonIds: ppl.Msg.GetIds()})); err != nil {
			return "", nil, fmt.Errorf("assign: %w", err)
		}
	}
	return id, emails, nil
}

// stored counts the candidate-written events in an attempt's log by kind and item.
func (s *client) stored(ctx context.Context, attempt string) (map[string]int, error) {
	log, err := s.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: attempt}))
	if err != nil {
		return nil, err
	}
	if !log.Msg.GetChainIntact() {
		return nil, errors.New("the chain does not verify")
	}
	got := map[string]int{}
	for _, e := range log.Msg.GetEvents() {
		got[e.GetKind()+"/"+e.GetItemId()]++
	}
	return got, nil
}

// ── plumbing ───────────────────────────────────────────────────────────────

type client struct {
	auth      assessmatrixv1connect.AuthServiceClient
	exams     assessmatrixv1connect.ExamServiceClient
	people    assessmatrixv1connect.PersonServiceClient
	assign    assessmatrixv1connect.AssignmentServiceClient
	candidate assessmatrixv1connect.CandidateServiceClient
	results   assessmatrixv1connect.ResultServiceClient
}

// shared keeps connections pooled across the cohort; each candidate has its
// own cookie jar and address.
var shared = &http.Transport{MaxIdleConnsPerHost: 1024, IdleConnTimeout: 90 * time.Second}

type from struct{ addr string }

func (f from) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Forwarded-For", f.addr)
	return shared.RoundTrip(r)
}

func newClient(addr string) *client {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Transport: from{addr}, Timeout: 30 * time.Second}
	u := *base
	return &client{
		auth:      assessmatrixv1connect.NewAuthServiceClient(hc, u),
		exams:     assessmatrixv1connect.NewExamServiceClient(hc, u),
		people:    assessmatrixv1connect.NewPersonServiceClient(hc, u),
		assign:    assessmatrixv1connect.NewAssignmentServiceClient(hc, u),
		candidate: assessmatrixv1connect.NewCandidateServiceClient(hc, u),
		results:   assessmatrixv1connect.NewResultServiceClient(hc, u),
	}
}

// waitMail polls the dev mailbox for the newest mail of a kind to one address.
func waitMail(ctx context.Context, to, kind string, re *regexp.Regexp) (string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		res, err := http.Get(*base + "/dev/mailbox?to=" + url.QueryEscape(to))
		if err == nil {
			var msgs []struct{ To, Body, Kind string }
			err = json.NewDecoder(res.Body).Decode(&msgs)
			res.Body.Close()
			if err == nil {
				for _, m := range msgs {
					if m.Kind == kind {
						if g := re.FindStringSubmatch(m.Body); g != nil {
							return g[1], nil
						}
					}
				}
			}
		}
		time.Sleep(time.Duration(300+rand.IntN(400)) * time.Millisecond)
	}
	return "", fmt.Errorf("no %s mail to %s", kind, to)
}

func refusal(r *connect.Response[v1.OpenLinkResponse]) string {
	if r == nil {
		return ""
	}
	return r.Msg.GetRefusal().String()
}

func allItems(e *v1.Exam) []*v1.Item {
	var out []*v1.Item
	for _, s := range e.GetSections() {
		out = append(out, s.GetItems()...)
	}
	return out
}

var (
	mu    sync.Mutex
	took  = map[string][]time.Duration{}
	codes = map[string]map[string]int{}
	calls atomic.Int64
)

func timed[T any](name string, call func() (*connect.Response[T], error)) (*connect.Response[T], error) {
	start := time.Now()
	r, err := call()
	d := time.Since(start)
	calls.Add(1)
	code := "ok"
	if err != nil {
		code = connect.CodeOf(err).String()
	}
	mu.Lock()
	took[name] = append(took[name], d)
	if codes[name] == nil {
		codes[name] = map[string]int{}
	}
	codes[name][code]++
	mu.Unlock()
	return r, err
}

func report(wall time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Printf("\n%d calls in %s (%.1f/s)\n", calls.Load(), wall.Round(time.Second), float64(calls.Load())/wall.Seconds())
	fmt.Printf("%-14s %7s %8s %8s %8s %8s  %s\n", "procedure", "calls", "p50", "p95", "p99", "max", "outcomes")
	names := make([]string, 0, len(took))
	for k := range took {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		ds := took[k]
		slices.Sort(ds)
		q := func(p float64) time.Duration {
			return ds[min(len(ds)-1, int(p*float64(len(ds))))].Round(time.Millisecond)
		}
		var out []string
		for c, m := range codes[k] {
			out = append(out, fmt.Sprintf("%s=%d", c, m))
		}
		sort.Strings(out)
		fmt.Printf("%-14s %7d %8s %8s %8s %8s  %s\n", k, len(ds), q(.5), q(.95), q(.99), ds[len(ds)-1].Round(time.Millisecond), strings.Join(out, " "))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "loadsim:", err)
	os.Exit(1)
}
