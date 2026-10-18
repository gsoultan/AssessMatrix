package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/webhook"
)

// receiver is an organisation's endpoint: it checks every signature the way
// docs/05 tells a receiver to, and remembers what arrived.
type receiver struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	secret string
	status int
	got    []received
}

type received struct {
	eventID, eventType string
	body               []byte
	env                envelope
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{t: t, status: http.StatusNoContent}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		defer r.mu.Unlock()
		if !webhook.Verify(r.secret, req.Header.Get(webhook.HeaderSignature), body, time.Now(), 5*time.Minute) {
			t.Errorf("a delivery arrived with a bad signature: %q", req.Header.Get(webhook.HeaderSignature))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Errorf("a delivery is not JSON: %v", err)
		}
		if env.ID != req.Header.Get(webhook.HeaderEventID) || env.Type != req.Header.Get(webhook.HeaderEventType) {
			t.Errorf("headers %s/%s disagree with the body %s/%s", req.Header.Get(webhook.HeaderEventID),
				req.Header.Get(webhook.HeaderEventType), env.ID, env.Type)
		}
		r.got = append(r.got, received{eventID: env.ID, eventType: env.Type, body: body, env: env})
		w.WriteHeader(r.status)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, g := range r.got {
		out = append(out, g.eventType)
	}
	return out
}

func (r *receiver) setStatus(s int) { r.mu.Lock(); r.status = s; r.mu.Unlock() }

// local lets the harness register and reach a receiver on 127.0.0.1.
var local = webhook.Policy{AllowPrivate: true}

// drain delivers every pending row, as the worker would, once each.
func (h *harness) drain(last bool) []error {
	h.t.Helper()
	ctx := context.Background()
	rows, err := h.owner.Query(ctx, `SELECT id::text FROM webhook_deliveries WHERE state = 1 ORDER BY created_at, id`)
	if err != nil {
		h.t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		h.t.Fatal(err)
	}
	var errs []error
	for _, id := range ids {
		if err := webhook.Deliver(ctx, h.srv.DB, h.srv.Keys, webhook.NewClient(local), id, last); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (h *harness) subscribe(staff *client, url string, events ...string) (*v1.WebhookEndpoint, string) {
	h.t.Helper()
	if len(events) == 0 {
		events = EventTypes
	}
	res := get(staff.hooks.CreateWebhook(context.Background(), connect.NewRequest(&v1.CreateWebhookRequest{
		Url: url, Description: "HRIS", Events: events})))
	return res.Msg.GetEndpoint(), res.Msg.GetSecret()
}

// The whole lifecycle reaches the receiver, signed, with ids and no personal
// data or live link in any payload.
func TestWebhooksCarryTheLifecycleSigned(t *testing.T) {
	h := newHarness(t)
	h.srv.WebhookPolicy = local
	ctx := context.Background()
	staff := h.tenant("Hooks")
	rcv := newReceiver(t)
	ep, secret := h.subscribe(staff, rcv.srv.URL+"/am")
	rcv.secret = secret
	if !strings.HasPrefix(secret, "whsec_") || ep.GetSecretHint() != secret[:10] || ep.GetSecretHint() == secret {
		t.Fatalf("secret %q, hint %q", secret, ep.GetSecretHint())
	}
	// The secret is at rest only sealed.
	if n := h.count(`SELECT count(*) FROM webhook_endpoints WHERE strpos(encode(secret, 'escape'), $1) > 0`, secret[6:]); n != 0 {
		t.Fatal("the webhook secret is stored in the clear")
	}

	f := h.publishedFixture(staff)
	token := h.lastLink(f.email)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	// Resuming is not a second start.
	get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents})))
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}}}})))
	get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
	get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 5})))

	if errs := h.drain(false); len(errs) > 0 {
		t.Fatalf("deliveries failed: %v", errs)
	}
	want := []string{EventExamPublished, EventAssignmentInvited, EventAttemptStarted, EventAttemptSubmitted,
		EventAttemptScored, EventCredentialIssued}
	// Delivery is at least once, not in order: receivers order by created_at.
	got := rcv.types()
	slices.Sort(got)
	if exp := slices.Sorted(slices.Values(want)); !slices.Equal(got, exp) {
		t.Fatalf("received %v, want %v", got, exp)
	}
	for _, g := range rcv.got {
		for _, secretish := range []string{f.email, token, h.lastCode(f.email), "Budi"} {
			if bytes.Contains(g.body, []byte(secretish)) {
				t.Errorf("%s carries personal data or a live credential: %s", g.eventType, g.body)
			}
		}
	}
	var sub map[string]any
	for _, g := range rcv.got {
		if g.eventType == EventAttemptSubmitted {
			sub = g.env.Data
		}
	}
	if sub["attempt_id"] != att.GetId() || sub["person_id"] != f.person.GetId() || sub["exam_id"] != f.exam.GetId() {
		t.Errorf("attempt.submitted names the wrong records: %v", sub)
	}
	if sc, _ := sub["score"].(map[string]any); sc == nil || sc["pending_review"] != true {
		t.Errorf("attempt.submitted should say the essay is still to be scored: %v", sub["score"])
	}

	// A second drain sends nothing: every row is delivered.
	h.drain(false)
	if n := len(rcv.types()); n != len(want) {
		t.Fatalf("a delivered event was sent again: %d deliveries", n)
	}
	list := get(staff.hooks.ListWebhooks(ctx, connect.NewRequest(&v1.ListWebhooksRequest{}))).Msg.GetEndpoints()
	if len(list) != 1 || list[0].GetDelivered() != int32(len(want)) || list[0].GetFailing() != 0 || list[0].GetLastDeliveryAt() == 0 {
		t.Fatalf("endpoint health = %+v", list)
	}
}

// An event exists only if the change it reports commits.
func TestWebhookEventOnlyIfTheChangeCommits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("HookRollback")
	h.subscribe(staff, "https://hooks.example.test/am")
	boom := errors.New("the change failed")
	err := h.srv.DB.InTenant(ctx, db.TenantID(h.lastTenant), func(tx *db.Tx) error {
		if err := h.srv.emit(ctx, tx, EventExamPublished, "", map[string]any{"exam_id": "x"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want the change's error back, got %v", err)
	}
	if n := h.count(`SELECT count(*) FROM webhook_events WHERE tenant_id = $1`, h.lastTenant); n != 0 {
		t.Fatalf("a rolled-back change left %d webhook event(s)", n)
	}
	// And with nobody subscribed, nothing is written at all.
	other := h.tenant("HookQuiet")
	h.publishedFixture(other)
	if n := h.count(`SELECT count(*) FROM webhook_events WHERE tenant_id = $1`, h.lastTenant); n != 0 {
		t.Fatalf("a tenant with no webhooks stored %d event(s)", n)
	}
}

// A failing receiver is retried, then given up on, and a redelivery sends the
// same event id and the same bytes.
func TestWebhookRetryGiveUpAndRedeliver(t *testing.T) {
	h := newHarness(t)
	h.srv.WebhookPolicy = local
	ctx := context.Background()
	staff := h.tenant("HookRetry")
	rcv := newReceiver(t)
	ep, secret := h.subscribe(staff, rcv.srv.URL, EventExamPublished)
	rcv.secret = secret
	rcv.setStatus(http.StatusInternalServerError)
	h.publishedFixture(staff)

	if errs := h.drain(false); len(errs) != 1 {
		t.Fatalf("a 500 must be reported so the job retries, got %v", errs)
	}
	ds := get(staff.hooks.ListDeliveries(ctx, connect.NewRequest(&v1.ListDeliveriesRequest{EndpointId: ep.GetId()}))).Msg.GetDeliveries()
	if len(ds) != 1 || ds[0].GetState() != v1.DeliveryState_DELIVERY_STATE_PENDING || ds[0].GetTries() != 1 ||
		ds[0].GetLastStatus() != 500 || ds[0].GetLastError() == "" {
		t.Fatalf("after one failure: %+v", ds)
	}
	h.drain(true)
	failed := get(staff.hooks.ListDeliveries(ctx, connect.NewRequest(&v1.ListDeliveriesRequest{FailedOnly: true}))).Msg.GetDeliveries()
	if len(failed) != 1 || failed[0].GetTries() != 2 {
		t.Fatalf("out of tries, it must be marked failed: %+v", failed)
	}
	if h.drain(false) != nil {
		t.Fatal("a failed delivery must not be sent again unless asked")
	}

	rcv.setStatus(http.StatusOK)
	re := get(staff.hooks.Redeliver(ctx, connect.NewRequest(&v1.RedeliverRequest{DeliveryId: failed[0].GetId()}))).Msg.GetDelivery()
	if re.GetState() != v1.DeliveryState_DELIVERY_STATE_PENDING {
		t.Fatalf("redeliver should queue it again: %+v", re)
	}
	if errs := h.drain(false); len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rcv.got) != 3 || rcv.got[0].eventID != rcv.got[2].eventID || !bytes.Equal(rcv.got[0].body, rcv.got[2].body) {
		t.Fatal("a redelivery must carry the original event id and body")
	}
	done := get(staff.hooks.ListDeliveries(ctx, connect.NewRequest(&v1.ListDeliveriesRequest{}))).Msg.GetDeliveries()[0]
	if done.GetState() != v1.DeliveryState_DELIVERY_STATE_DELIVERED || done.GetDeliveredAt() == 0 {
		t.Fatalf("after redelivery: %+v", done)
	}

	// Paused: a ping is refused, and nothing new is queued for it.
	get(staff.hooks.UpdateWebhook(ctx, connect.NewRequest(&v1.UpdateWebhookRequest{Id: ep.GetId(), Url: ep.GetUrl(),
		Events: ep.GetEvents(), Active: false})))
	if _, err := staff.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: ep.GetId()})); code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("pinging a paused webhook: %v", err)
	}
	h.publishedFixture(staff)
	if n := h.count(`SELECT count(*) FROM webhook_deliveries WHERE endpoint_id = $1`, ep.GetId()); n != 1 {
		t.Fatalf("a paused webhook got %d deliveries, want the 1 from before", n)
	}

	// Rotation: the old secret stops verifying.
	get(staff.hooks.UpdateWebhook(ctx, connect.NewRequest(&v1.UpdateWebhookRequest{Id: ep.GetId(), Url: ep.GetUrl(),
		Events: ep.GetEvents(), Active: true})))
	rot := get(staff.hooks.RotateWebhookSecret(ctx, connect.NewRequest(&v1.RotateWebhookSecretRequest{Id: ep.GetId()}))).Msg
	if rot.GetSecret() == secret || rot.GetEndpoint().GetSecretHint() != rot.GetSecret()[:10] {
		t.Fatal("rotation must issue a new secret")
	}
	rcv.secret = rot.GetSecret()
	get(staff.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: ep.GetId()})))
	if errs := h.drain(false); len(errs) > 0 {
		t.Fatalf("a ping signed with the new secret: %v", errs)
	}
	if last := rcv.types(); last[len(last)-1] != EventWebhookPing {
		t.Fatalf("the ping did not arrive: %v", last)
	}

	// Deleting the endpoint drops its log; a job still queued finds nothing.
	ping := get(staff.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: ep.GetId()}))).Msg.GetDelivery()
	get(staff.hooks.DeleteWebhook(ctx, connect.NewRequest(&v1.DeleteWebhookRequest{Id: ep.GetId()})))
	if err := webhook.Deliver(ctx, h.srv.DB, h.srv.Keys, webhook.NewClient(local), ping.GetId(), false); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("a delivery of a deleted webhook should cancel: %v", err)
	}
}

// Receivers are URLs a tenant types in: the core must not be pointed at
// itself, its database or the cloud metadata service.
func TestWebhookRefusesPrivateAddresses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("HookSSRF")
	for _, u := range []string{
		"http://hooks.example.test/am", "https://127.0.0.1/x", "https://localhost/x", "https://169.254.169.254/latest/meta-data",
		"https://10.0.0.5/x", "https://[::1]/x", "https://[::ffff:127.0.0.1]/x", "https://100.64.0.1/x",
		"https://user:pass@hooks.example.test/x", "ftp://hooks.example.test/x", "hooks.example.test/x", "",
	} {
		_, err := staff.hooks.CreateWebhook(ctx, connect.NewRequest(&v1.CreateWebhookRequest{Url: u, Events: []string{EventAttemptStarted}}))
		if code(err) != connect.CodeInvalidArgument {
			t.Errorf("%q: want InvalidArgument, got %v", u, err)
		}
	}
	if _, err := staff.hooks.CreateWebhook(ctx, connect.NewRequest(&v1.CreateWebhookRequest{
		Url: "https://hooks.example.test/x", Events: []string{"attempt.everything"}})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown event type: %v", err)
	}

	// A name that resolves inward passes the form but is refused on connect.
	rcv := newReceiver(t)
	strict := webhook.NewClient(webhook.Policy{})
	h.srv.WebhookPolicy = local
	ep, secret := h.subscribe(staff, rcv.srv.URL)
	rcv.secret = secret
	d := get(staff.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: ep.GetId()}))).Msg.GetDelivery()
	if err := webhook.Deliver(ctx, h.srv.DB, h.srv.Keys, strict, d.GetId(), false); err == nil {
		t.Fatal("the strict client reached a loopback receiver")
	}
	if len(rcv.got) != 0 {
		t.Fatal("the loopback receiver got a request")
	}
	got := get(staff.hooks.ListDeliveries(ctx, connect.NewRequest(&v1.ListDeliveriesRequest{}))).Msg.GetDeliveries()[0]
	if !strings.Contains(got.GetLastError(), "private or local") {
		t.Fatalf("the reason should say why: %q", got.GetLastError())
	}

	// Redirects are not followed: a 3xx is a failed delivery.
	redir := httptest.NewServer(http.RedirectHandler(rcv.srv.URL, http.StatusFound))
	t.Cleanup(redir.Close)
	ep2, _ := h.subscribe(staff, redir.URL)
	d2 := get(staff.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: ep2.GetId()}))).Msg.GetDelivery()
	if err := webhook.Deliver(ctx, h.srv.DB, h.srv.Keys, webhook.NewClient(local), d2.GetId(), false); err == nil || len(rcv.got) != 0 {
		t.Fatalf("a redirect was followed (err %v, received %d)", err, len(rcv.got))
	}

	for _, c := range []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true}, {"2606:4700::1111", true}, {"127.0.0.1", false}, {"10.1.2.3", false}, {"172.16.0.1", false},
		{"192.168.1.1", false}, {"169.254.169.254", false}, {"100.100.100.200", false}, {"::1", false}, {"fd00::1", false},
		{"fe80::1", false}, {"::ffff:10.0.0.1", false}, {"64:ff9b::a00:1", false}, {"0.0.0.0", false}, {"224.0.0.1", false},
	} {
		if got := webhook.Public(netip.MustParseAddr(c.ip)); got != c.public {
			t.Errorf("Public(%s) = %v, want %v", c.ip, got, c.public)
		}
	}
}

func TestWebhookLimitPerTenant(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("HookLimit")
	for range maxWebhooksPerTenant {
		h.subscribe(staff, "https://hooks.example.test/am")
	}
	_, err := staff.hooks.CreateWebhook(ctx, connect.NewRequest(&v1.CreateWebhookRequest{
		Url: "https://hooks.example.test/am", Events: []string{EventAttemptStarted}}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("webhook %d: want FailedPrecondition, got %v", maxWebhooksPerTenant+1, err)
	}
}

// Erasure takes the webhook log about the person with it; retention sweeps
// the rest after WebhookKeep.
func TestWebhookLogIsErasedAndSwept(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("HookErase")
	h.subscribe(staff, "https://hooks.example.test/am")
	f := h.publishedFixture(staff)
	if n := h.count(`SELECT count(*) FROM webhook_events WHERE person_id = $1`, f.person.GetId()); n != 1 {
		t.Fatalf("fixture: want the invitation event about the person, got %d", n)
	}
	get(staff.people.ErasePerson(ctx, connect.NewRequest(&v1.ErasePersonRequest{Id: f.person.GetId(), Reason: "request"})))
	if n := h.count(`SELECT count(*) FROM webhook_events WHERE person_id = $1`, f.person.GetId()); n != 0 {
		t.Fatalf("erasure left %d webhook event(s) about the person", n)
	}
	if n := h.count(`SELECT count(*) FROM webhook_events WHERE tenant_id = $1`, h.lastTenant); n != 1 {
		t.Fatalf("the exam.published event should stay until swept, got %d", n)
	}
	h.clock.add(WebhookKeep + time.Hour)
	if _, err := h.srv.PurgeDue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT count(*) FROM webhook_events WHERE tenant_id = $1`, h.lastTenant); n != 0 {
		t.Fatalf("retention left %d webhook event(s) past %s", n, WebhookKeep)
	}
}
