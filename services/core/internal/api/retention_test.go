package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
)

// submitted takes a candidate through a whole attempt: q1 right, an essay
// with an uploaded image. Returns the attempt id and the image's URL.
func submitted(t *testing.T, h *harness, f *fixture) (string, string, *client) {
	t.Helper()
	ctx := context.Background()
	cand := h.redeem(f)
	st, img := cand.upload(t, pngBytes(t))
	if st != http.StatusCreated {
		t.Fatalf("upload: %d", st)
	}
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
		Events: []*v1.AttemptEvent{
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
			{Kind: "response.typed", ItemId: "q4", Text: "Sebuah esai."},
			{Kind: "answer.attached", ItemId: "q4", File: &v1.Media{Id: "m", Url: img}},
			{Kind: "focus.lost"},
		}})))
	get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
	return att.GetId(), img, cand
}

func (h *harness) count(q string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// The candidate is told their record is kept at most a year after the exam,
// then deleted (integrity.ts). After that, the events, the per-item index
// and their uploads are gone; the score and the fact of deletion stay.
func TestRetentionDeletesTheRecordAndKeepsTheScore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Retention")
	f := h.publishedFixture(staff)
	id, img, cand := submitted(t, h, f)
	scoreBefore := get(staff.results.ListResponses(ctx, connect.NewRequest(&v1.ListResponsesRequest{ExamId: f.exam.GetId()}))).Msg.GetResponses()[0].GetScore()

	// A day short of the period: nothing goes.
	h.clock.add(364 * 24 * time.Hour)
	// (Counts are not asserted: the test database holds other tests' attempts.)
	if _, err := h.srv.PurgeDue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT count(*) FROM attempt_events WHERE attempt_id = $1`, id); n == 0 {
		t.Fatal("purged a day before the period ended")
	}
	h.clock.add(2 * 24 * time.Hour)
	if _, err := h.srv.PurgeDue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT count(*) FROM attempt_events WHERE attempt_id = $1`, id); n != 0 {
		t.Fatalf("%d events left after retention", n)
	}
	if n := h.count(`SELECT count(*) FROM item_scores WHERE attempt_id = $1`, id); n != 0 {
		t.Fatalf("%d item scores (with the choices picked) left after retention", n)
	}
	if st, _, _ := staff.fetch(t, img); st != http.StatusNotFound {
		t.Fatalf("the candidate's uploaded image must be gone, got %d", st)
	}
	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: id}))).Msg
	if log.GetPurgedAt() == 0 || len(log.GetEvents()) != 0 || !log.GetChainIntact() || len(log.GetFlags()) != 0 {
		t.Fatalf("a purged log reads as deleted on purpose, not as tampered: %+v", log)
	}
	after := get(staff.results.ListResponses(ctx, connect.NewRequest(&v1.ListResponsesRequest{ExamId: f.exam.GetId()}))).Msg.GetResponses()
	if len(after) != 1 || after[0].GetScore().GetPct() != scoreBefore.GetPct() || len(after[0].GetAnswers()) != 0 {
		t.Fatalf("the score stays and the answers go, got %+v", after)
	}
	if n := h.count(`SELECT count(*) FROM audit_log WHERE action = 'attempt.purged' AND target = $1 AND detail->>'why' = 'retention'`, id); n != 1 {
		t.Fatal("the deletion must be in the audit log")
	}
	// Nothing can be rebuilt from a log that is gone.
	_, err := staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: id, ItemId: "q4", Points: 1}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("scoring a purged attempt: %v", err)
	}
	_, err = cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: id, BatchId: "late",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"a"}}}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("appending to a purged attempt: %v", err)
	}
	reviews := get(staff.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId(), IncludeDone: true}))).Msg.GetTasks()
	if len(reviews) != 0 {
		t.Fatal("a purged essay cannot be reviewed")
	}
	// Run again: nothing more to do, nothing breaks.
	if _, err := h.srv.PurgeDue(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n := h.count(`SELECT count(*) FROM audit_log WHERE action = 'attempt.purged' AND target = $1`, id); n != 1 {
		t.Fatalf("purged %d times", n)
	}
}

// The log stays append-only for everyone but the purge: the application
// cannot delete, the owner cannot delete by accident, nobody can edit.
func TestEventsStayAppendOnlyOutsideThePurge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("AppendOnly")
	f := h.publishedFixture(staff)
	id, _, _ := submitted(t, h, f)
	if _, err := h.owner.Exec(ctx, `DELETE FROM attempt_events WHERE attempt_id = $1`, id); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("the owner deleted events outside the purge: %v", err)
	}
	tx, err := h.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('am.purging', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE attempt_events SET kind = kind WHERE attempt_id = $1`, id); err == nil {
		t.Fatal("an event was edited under the purge flag")
	}
	tx.Rollback(ctx)
	// The application role, even under its own tenant, cannot purge another tenant's attempt.
	h.tenant("Other")
	err = h.srv.DB.InTenant(ctx, db.TenantID(h.lastTenant), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT am_purge_attempt($1)`, id)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a tenant purged another tenant's attempt: %v", err)
	}
	if n := h.count(`SELECT count(*) FROM attempt_events WHERE attempt_id = $1`, id); n == 0 {
		t.Fatal("events went")
	}
}

// Erasure on request: the person, their attempts, credentials, uploads and
// mail go; the audit log says so by id, with no address in it.
func TestErasureRemovesEverythingAboutThePerson(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Erase")
	f := h.publishedFixture(staff)
	id, img, _ := submitted(t, h, f)
	rev := get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: id, ItemId: "q4", Points: 5}))).Msg
	credID := strings.TrimPrefix(rev.GetCredentialUrl(), "http://exam.test/verify/#")
	if credID == "" {
		t.Fatal("fixture: expected a credential")
	}

	if _, err := staff.people.ErasePerson(ctx, connect.NewRequest(&v1.ErasePersonRequest{Id: f.person.GetId()})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an erasure needs a reason for the audit log: %v", err)
	}
	out := get(staff.people.ErasePerson(ctx, connect.NewRequest(&v1.ErasePersonRequest{Id: f.person.GetId(), Reason: "candidate request #12"}))).Msg
	if out.GetAttempts() != 1 || out.GetCredentials() != 1 || out.GetImages() != 1 {
		t.Fatalf("erased %+v, want 1 attempt, 1 credential, 1 image", out)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM people WHERE id = $1`:                                     0,
		`SELECT count(*) FROM attempts WHERE person_id = $1`:                            0,
		`SELECT count(*) FROM assignments WHERE person_id = $1`:                         0,
		`SELECT count(*) FROM audit_log WHERE action = 'person.erased' AND target = $1`: 1,
	} {
		if n := h.count(q, f.person.GetId()); n != want {
			t.Errorf("%s: %d, want %d", q, n, want)
		}
	}
	if n := h.count(`SELECT count(*) FROM attempt_events WHERE attempt_id = $1`, id); n != 0 {
		t.Errorf("%d events left", n)
	}
	if n := h.count(`SELECT count(*) FROM outbox WHERE lower(to_addr) = lower($1)`, f.email); n != 0 {
		t.Errorf("%d mails to them left", n)
	}
	if n := h.count(`SELECT count(*) FROM audit_log WHERE detail::text ILIKE '%' || $1 || '%'`, f.email); n != 0 {
		t.Error("the audit log must not carry the address")
	}
	if st, _, _ := staff.fetch(t, img); st != http.StatusNotFound {
		t.Errorf("their image: %d", st)
	}
	var pub struct{}
	if code := fetchJSON(t, h.http.URL+"/credentials/"+credID, &pub); code != http.StatusNotFound {
		t.Errorf("their credential still answers: %d", code)
	}
	if _, err := staff.people.ErasePerson(ctx, connect.NewRequest(&v1.ErasePersonRequest{Id: f.person.GetId(), Reason: "again"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("erasing twice: %v", err)
	}
}
