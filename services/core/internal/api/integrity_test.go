package api

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// A tier that collects signals cannot start until each purpose is agreed to,
// and the agreement is on the record.
func TestStartNeedsConsentForEachPurpose(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Consent")
	f := h.publishedFixture(staff) // an exam: tier 2
	cand := h.redeem(f)

	_, err := cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: []string{domain.PurposeSignals}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "camera") {
		t.Fatalf("a tier 2 start without camera consent must be refused, naming it; got %v", err)
	}
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()})))
	recorded := map[string]bool{}
	for _, ev := range log.Msg.GetEvents() {
		recorded[ev.GetKind()] = true
	}
	for _, p := range domain.RequiredConsents(2) {
		if !recorded[domain.KindConsentPrefix+p] {
			t.Errorf("consent to %s is not on the record", p)
		}
	}

	// A client cannot write a consent of its own.
	_, err = cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "forge",
		Events: []*v1.AttemptEvent{{Kind: domain.KindConsentPrefix + domain.PurposeCamera}}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a client-written consent must be refused, got %v", err)
	}
}

// A signal collected under a purpose the tier does not ask for is never
// stored: a tier 1 quiz holds no camera events, whatever the client sends.
// It is dropped, not refused, so the answers sent with it still land.
func TestNoSignalWithoutItsConsent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Quiz")
	f := h.publishedFixture(staff)
	// Down to tier 1 (signals only) as a new published version.
	e := get(staff.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: f.exam.GetId()}))).Msg.GetExam()
	e.Tier = 1
	get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_IN_PLACE, ExpectedUpdatedAt: e.GetUpdatedAt()})))
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "cam",
		Events: []*v1.AttemptEvent{
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
			{Kind: domain.KindFaceMissing, Values: []string{"40000"}},
		}})))
	kinds := map[string]bool{}
	for _, ev := range get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg.GetEvents() {
		kinds[ev.GetKind()] = true
	}
	if kinds[domain.KindFaceMissing] || kinds["consent.camera"] {
		t.Fatal("a tier 1 attempt must hold no camera consent and no camera events")
	}
	if !kinds["response.changed"] {
		t.Fatal("the answer sent alongside an unconsented signal must still be saved")
	}
	// Signals it does collect go in, and become flags with their reasons.
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "sig",
		Events: []*v1.AttemptEvent{{Kind: domain.KindPasted, ItemId: "q3", Values: []string{"320"}}}})))
	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()})))
	var paste *v1.Flag
	for _, fl := range log.Msg.GetFlags() {
		if fl.GetType() == domain.FlagPaste {
			paste = fl
		}
	}
	if paste == nil || !strings.Contains(paste.GetReason(), "question 3") || !strings.Contains(paste.GetReasonId(), "320") {
		t.Fatalf("a 320-character paste into question 3 must be flagged with that reason, got %+v", log.Msg.GetFlags())
	}
}
