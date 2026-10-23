package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1/assessmatrixv1connect"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/limit"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// These tests run against a real Postgres, as the application role, with
// row-level security on. Set AM_TEST_DATABASE_URL (the unprivileged role)
// and AM_TEST_MIGRATE_DATABASE_URL (the schema owner); see services/core/README.md.

var (
	migrateOnce sync.Once
	migrateErr  error
)

type harness struct {
	t     *testing.T
	srv   *Server
	http  *httptest.Server
	owner *pgx.Conn
	clock *clock
	// lastTenant is the id of the tenant most recently made by tenant().
	lastTenant string
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	appURL, ownerURL := os.Getenv("AM_TEST_DATABASE_URL"), os.Getenv("AM_TEST_MIGRATE_DATABASE_URL")
	if appURL == "" || ownerURL == "" {
		if os.Getenv("CI") != "" {
			// In CI a missing database is a broken pipeline, not a pass.
			t.Fatal("AM_TEST_DATABASE_URL and AM_TEST_MIGRATE_DATABASE_URL must be set in CI")
		}
		t.Skip("AM_TEST_DATABASE_URL and AM_TEST_MIGRATE_DATABASE_URL are not set")
	}
	ctx := context.Background()
	migrateOnce.Do(func() { _, migrateErr = db.Migrate(ctx, ownerURL, "am_app") })
	if migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}
	database, err := db.Open(ctx, appURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := database.CheckRole(ctx); err != nil {
		t.Fatalf("the test database role must be unprivileged: %v", err)
	}
	owner, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(context.Background()) })

	keys, err := secure.NewKeys([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Now()}
	srv := &Server{
		DB: database, Keys: keys,
		Auth:      &auth.Authenticator{DB: database, Keys: keys, Policy: Policy()},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		PublicURL: "http://exam.test",
		Now:       clk.now,
	}
	// Tests open many links from one address; the limiter has its own test.
	srv.ipLimit, srv.emailLimit = limit.New(1_000_000, time.Minute, 1000), limit.New(1_000_000, time.Minute, 100_000)
	srv.tenantLimit = limit.New(1_000_000, time.Minute, 1000)
	srv.Media = media.Regional{Store: media.FS{Dir: t.TempDir()}, Region: "id"}
	// Mounted as cmd/core mounts them: RPCs by prefix, images on their own paths.
	rpc, images, public := srv.Handler(), srv.MediaHandler(), srv.PublicHandler()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/media") {
			images.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/credentials/") || strings.HasPrefix(r.URL.Path, "/issuers/") {
			public.ServeHTTP(w, r)
			return
		}
		rpc.ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	return &harness{t: t, srv: srv, http: hs, owner: owner, clock: clk}
}

// client is one browser: its own cookie jar.
type client struct {
	// email is the staff address this browser signed in with, if any.
	email     string
	http      *http.Client
	base      string
	auth      assessmatrixv1connect.AuthServiceClient
	exams     assessmatrixv1connect.ExamServiceClient
	people    assessmatrixv1connect.PersonServiceClient
	assign    assessmatrixv1connect.AssignmentServiceClient
	candidate assessmatrixv1connect.CandidateServiceClient
	results   assessmatrixv1connect.ResultServiceClient
	proctor   assessmatrixv1connect.ProctorServiceClient
	hooks     assessmatrixv1connect.WebhookServiceClient
	tenants   assessmatrixv1connect.TenantServiceClient
	setup     assessmatrixv1connect.SetupServiceClient
}

func (h *harness) browser() *client {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	u := h.http.URL
	return &client{
		http:      hc,
		base:      u,
		auth:      assessmatrixv1connect.NewAuthServiceClient(hc, u),
		exams:     assessmatrixv1connect.NewExamServiceClient(hc, u),
		people:    assessmatrixv1connect.NewPersonServiceClient(hc, u),
		assign:    assessmatrixv1connect.NewAssignmentServiceClient(hc, u),
		candidate: assessmatrixv1connect.NewCandidateServiceClient(hc, u),
		results:   assessmatrixv1connect.NewResultServiceClient(hc, u),
		proctor:   assessmatrixv1connect.NewProctorServiceClient(hc, u),
		hooks:     assessmatrixv1connect.NewWebhookServiceClient(hc, u),
		tenants:   assessmatrixv1connect.NewTenantServiceClient(hc, u),
		setup:     assessmatrixv1connect.NewSetupServiceClient(hc, u),
	}
}

// tenant creates a tenant and a signed-in staff browser for it.
func (h *harness) tenant(name string) *client {
	h.t.Helper()
	email := strings.ReplaceAll(strings.ToLower(name), " ", "-") + "-" + secure.Token()[:8] + "@example.test"
	h.newTenant(name, email)
	return h.signIn(email)
}

// newTenant creates a tenant with email as its administrator, signing nobody in.
func (h *harness) newTenant(name, email string) string {
	h.t.Helper()
	ctx := context.Background()
	var tenant string
	if err := h.owner.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenant); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.owner.Exec(ctx, `INSERT INTO staff (tenant_id, email, role) VALUES ($1, $2, 'admin')`, tenant, email); err != nil {
		h.t.Fatal(err)
	}
	h.lastTenant = tenant
	return tenant
}

// signIn signs an address in, in a fresh browser, the way the studio does.
func (h *harness) signIn(email string) *client {
	h.t.Helper()
	ctx := context.Background()
	c := h.browser()
	get(c.auth.RequestStaffCode(ctx, connect.NewRequest(&v1.RequestStaffCodeRequest{Email: email})))
	code := h.lastCode(email)
	res := get(c.auth.VerifyStaffCode(ctx, connect.NewRequest(&v1.VerifyStaffCodeRequest{Email: email, Code: code})))
	if !res.Msg.GetOk() {
		h.t.Fatal("staff sign-in failed")
	}
	c.email = email
	return c
}

var (
	codeRe = regexp.MustCompile(`\b(\d{6})\b`)
	linkRe = regexp.MustCompile(`#/i/([A-Za-z0-9_-]{43})`)
)

// mail returns the newest unsent outbox body for an address.
func (h *harness) mail(to, kind string) string {
	h.t.Helper()
	var body string
	err := h.owner.QueryRow(context.Background(), `SELECT body FROM outbox WHERE to_addr = $1 AND kind = $2
		ORDER BY created_at DESC LIMIT 1`, to, kind).Scan(&body)
	if err != nil {
		h.t.Fatalf("no %s mail to %s: %v", kind, to, err)
	}
	return body
}

func (h *harness) lastCode(to string) string {
	h.t.Helper()
	var body string
	err := h.owner.QueryRow(context.Background(), `SELECT body FROM outbox WHERE to_addr = $1 AND kind IN ('code', 'staff-code')
		ORDER BY created_at DESC LIMIT 1`, to).Scan(&body)
	if err != nil {
		h.t.Fatalf("no code mailed to %s: %v", to, err)
	}
	return codeRe.FindStringSubmatch(body)[1]
}

// mailCount is how many messages have been queued for an address.
func (h *harness) mailCount(to string) int {
	h.t.Helper()
	var n int
	if err := h.owner.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE to_addr = $1`, to).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) lastLink(to string) string {
	h.t.Helper()
	m := linkRe.FindStringSubmatch(h.mail(to, "invite"))
	if m == nil {
		h.t.Fatalf("no link in the invitation to %s", to)
	}
	return m[1]
}

// get unwraps a response; a failed call fails the test with its error.
// (Go cannot infer a type parameter from a curried call, hence a panic,
// which the test runner reports with the stack.)
func get[T any](r *connect.Response[T], err error) *connect.Response[T] {
	if err != nil {
		panic(err)
	}
	return r
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// fixture is a published exam with one person assigned and invited.
type fixture struct {
	exam   *v1.Exam
	person *v1.Person
	assign *v1.Assignment
	email  string
}

// allConsents is every purpose a tier can ask for, as a candidate who agreed to all.
var allConsents = []string{domain.PurposeSignals, domain.PurposeFullscreen, domain.PurposeCamera}

// sampleExam has one item of each auto-scored kind, plus an essay.
func sampleExam(e *v1.Exam) *v1.Exam {
	e.Title, e.Timed = "Sample", true
	e.Sections = []*v1.Section{{Id: "s1", Name: "Bagian 1", Minutes: 30, Items: []*v1.Item{
		{Id: "q1", Type: v1.ItemType_ITEM_TYPE_SINGLE, Stem: "Pick b", Points: 2, Required: true,
			Choices: []*v1.Choice{{Id: "a", Text: "A"}, {Id: "b", Text: "B"}}, Correct: []string{"b"}},
		{Id: "q2", Type: v1.ItemType_ITEM_TYPE_MULTIPLE, Stem: "Pick a and c", Points: 1,
			Choices: []*v1.Choice{{Id: "a"}, {Id: "b"}, {Id: "c"}}, Correct: []string{"a", "c"}},
		{Id: "q3", Type: v1.ItemType_ITEM_TYPE_SHORT, Stem: "Capital of Indonesia", Points: 1, Correct: []string{"Jakarta"}},
		{Id: "q4", Type: v1.ItemType_ITEM_TYPE_ESSAY, Stem: "Explain", Points: 5},
	}}}
	return e
}

func (h *harness) publishedFixture(staff *client) *fixture {
	h.t.Helper()
	return h.publishedFixtureWith(staff, func(*v1.Exam) {})
}

// publishedFixtureWith is publishedFixture with the sample exam changed by
// edit before it is saved.
func (h *harness) publishedFixtureWith(staff *client, edit func(*v1.Exam)) *fixture {
	h.t.Helper()
	ctx := context.Background()
	e := get(staff.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_EXAM}))).Msg.GetExam()
	e = sampleExam(e)
	edit(e)
	e = get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_DRAFT}))).Msg.GetExam()
	e = get(staff.exams.SetExamStatus(ctx, connect.NewRequest(&v1.SetExamStatusRequest{Id: e.GetId(), Status: v1.Status_STATUS_PUBLISHED}))).Msg.GetExam()
	email := "cand-" + secure.Token()[:8] + "@example.test"
	p := get(staff.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Name: "Budi", Email: email}}))).Msg.GetPerson()
	res := get(staff.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: e.GetId(), PersonIds: []string{p.GetId()}})))
	if res.Msg.GetSent() != 1 || len(res.Msg.GetAssignments()) != 1 {
		h.t.Fatalf("assigning a published exam should mail one invitation, got sent=%d", res.Msg.GetSent())
	}
	return &fixture{exam: e, person: p, assign: res.Msg.GetAssignments()[0], email: email}
}

// redeem opens a link and verifies the code in a fresh browser.
func (h *harness) redeem(f *fixture) *client {
	h.t.Helper()
	ctx := context.Background()
	c := h.browser()
	token := h.lastLink(f.email)
	open := get(c.candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: token})))
	if !open.Msg.GetOk() {
		h.t.Fatalf("link refused: %v", open.Msg.GetRefusal())
	}
	sent := get(c.candidate.RequestCode(ctx, connect.NewRequest(&v1.RequestCodeRequest{Token: token})))
	if !sent.Msg.GetOk() {
		h.t.Fatalf("code refused: %v", sent.Msg.GetRefusal())
	}
	v := get(c.candidate.VerifyCode(ctx, connect.NewRequest(&v1.VerifyCodeRequest{Token: token, Code: h.lastCode(f.email)})))
	if !v.Msg.GetOk() {
		h.t.Fatalf("verify refused: %v", v.Msg.GetRefusal())
	}
	return c
}
