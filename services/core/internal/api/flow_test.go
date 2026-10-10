package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// The loop that must never break, end to end against the real schema:
// author → publish → invite → open → code → sit → submit → score → results.
func TestAuthorInviteSitScore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Flow")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)

	sess := get(cand.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{})))
	for _, s := range sess.Msg.GetExam().GetSections() {
		for _, it := range s.GetItems() {
			if len(it.GetCorrect()) > 0 {
				t.Fatalf("item %s reached the candidate with its answer key", it.GetId())
			}
		}
	}

	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	if att.GetDeadlineAt() == 0 {
		t.Fatal("a timed exam needs a server-side deadline")
	}
	again := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	if again.GetId() != att.GetId() {
		t.Fatal("starting twice must resume the same attempt, never a blank paper")
	}

	batch := &v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b1", Events: []*v1.AttemptEvent{
		{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}, ClientTime: time.Now().UnixMilli()},
		{Kind: "response.changed", ItemId: "q2", Values: []string{"c", "a"}},
		{Kind: "response.typed", ItemId: "q3", Text: "  jakarta "},
		{Kind: "focus.lost", Detail: "tab hidden 3s"},
	}}
	first := get(cand.candidate.AppendEvents(ctx, connect.NewRequest(batch)))
	retry := get(cand.candidate.AppendEvents(ctx, connect.NewRequest(batch)))
	if first.Msg.GetLastSeq() != retry.Msg.GetLastSeq() {
		t.Fatalf("a retried batch must not append twice: %d then %d", first.Msg.GetLastSeq(), retry.Msg.GetLastSeq())
	}

	_, err := cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b2",
		Events: []*v1.AttemptEvent{{Kind: "attempt.submitted"}}}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("a client must not write server-only events, got %v", err)
	}

	sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
	sc := sub.Msg.GetAttempt().GetScore()
	if !sub.Msg.GetOk() || sc.GetRaw() != 4 || sc.GetMax() != 4 || sc.GetPct() != 100 || !sc.GetPassed() || sc.GetPendingHuman() != 5 {
		t.Fatalf("score = %+v, want 4/4 100%% passed with 5 points pending review", sc)
	}
	resub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
	if resub.Msg.GetAttempt().GetSubmittedAt() != sub.Msg.GetAttempt().GetSubmittedAt() {
		t.Fatal("submitting twice must return the first result")
	}
	_, err = cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "late",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"a"}}}}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a submitted attempt must refuse more answers, got %v", err)
	}

	list := get(staff.assign.ListAssignments(ctx, connect.NewRequest(&v1.ListAssignmentsRequest{ExamId: f.exam.GetId()})))
	a := list.Msg.GetAssignments()[0]
	if a.GetStatus() != v1.AssignStatus_ASSIGN_STATUS_SUBMITTED || a.GetScorePct() != 100 || !a.GetPassed() {
		t.Fatalf("the score must land back on the assignment, got %+v", a)
	}
	resp := get(staff.results.ListResponses(ctx, connect.NewRequest(&v1.ListResponsesRequest{ExamId: f.exam.GetId()})))
	if len(resp.Msg.GetResponses()) != 1 {
		t.Fatalf("want one response, got %d", len(resp.Msg.GetResponses()))
	}
	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()})))
	if !log.Msg.GetChainIntact() {
		t.Fatal("an untouched attempt log must verify")
	}
	kinds := []string{}
	for _, ev := range log.Msg.GetEvents() {
		kinds = append(kinds, ev.GetKind())
	}
	want := []string{"attempt.started", "identity.verified", "consent.signals", "consent.fullscreen", "consent.camera",
		"response.changed", "response.changed", "response.typed", "focus.lost", "attempt.submitted"}
	if len(kinds) != len(want) {
		t.Fatalf("log = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("log = %v, want %v", kinds, want)
		}
	}
}

// Replay of a tampered attempt is still detected (the integrity profile's
// proof in AGENTS.md). Someone with database access edits an answer.
func TestTamperedAttemptIsDetected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Tamper")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	answer := get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"a"}}}}))).Msg.GetLastSeq()

	// The append-only trigger stops even the owner, so the attacker has to
	// switch it off first; that is what we simulate.
	if _, err := h.owner.Exec(ctx, `UPDATE attempt_events SET body = body WHERE attempt_id = $1`, att.GetId()); err == nil {
		t.Fatal("attempt_events must refuse updates")
	}
	// The wrong answer becomes the right one.
	forged := (&v1.AttemptEvent{Seq: answer, Kind: "response.changed", ItemId: "q1", Values: []string{"b"}})
	body, _ := eventBody(forged)
	tx, err := h.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE attempt_events DISABLE TRIGGER attempt_events_append_only`} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE attempt_events SET body = $2 WHERE attempt_id = $1 AND seq = $3`, att.GetId(), body, answer); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE attempt_events ENABLE TRIGGER attempt_events_append_only`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()})))
	if log.Msg.GetChainIntact() || log.Msg.GetBrokenAtSeq() != answer {
		t.Fatalf("tampering at seq %d must be detected, got intact=%v broken_at=%d", answer, log.Msg.GetChainIntact(), log.Msg.GetBrokenAtSeq())
	}
	// And it is a flag a reviewer cannot miss, saying where the record breaks.
	var altered *v1.Flag
	for _, f := range log.Msg.GetFlags() {
		if f.GetType() == domain.FlagRecordAltered {
			altered = f
		}
	}
	if altered == nil || altered.GetSeverity() != domain.SeveritySerious || !strings.Contains(altered.GetReason(), fmt.Sprint(answer)) {
		t.Fatalf("a tampered record must raise a serious record_altered flag naming event %d, got %+v", answer, log.Msg.GetFlags())
	}
}

// Late answers: the server clock decides, with a bounded allowance for work
// done offline before the deadline.
func TestDeadlineIsServerTime(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Clock")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	deadline := time.UnixMilli(att.GetDeadlineAt())

	// Early forcing is ignored: required q1 is blank, so submit is refused.
	early := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId(), Force: true})))
	if early.Msg.GetOk() || len(early.Msg.GetMissingItemIds()) != 1 {
		t.Fatal("force before the server deadline must not skip the required-question check")
	}

	h.clock.add(time.Until(deadline) + 10*time.Minute)
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "offline",
		Events: []*v1.AttemptEvent{
			// Answered offline a minute before the deadline, synced ten minutes after: counts.
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}, ClientTime: deadline.Add(-time.Minute).UnixMilli()},
			// Claims to be on time but the device says after the deadline: does not count.
			{Kind: "response.typed", ItemId: "q3", Text: "Jakarta", ClientTime: deadline.Add(time.Minute).UnixMilli()},
		}})))
	sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId(), Force: true})))
	got := sub.Msg.GetAttempt()
	if _, ok := got.GetAnswers()["q1"]; !ok {
		t.Fatal("work done offline before the deadline must not be lost")
	}
	if _, ok := got.GetAnswers()["q3"]; ok {
		t.Fatal("an answer made after the deadline must not count")
	}
	if got.GetScore().GetRaw() != 2 {
		t.Fatalf("raw = %d, want 2", got.GetScore().GetRaw())
	}
}
