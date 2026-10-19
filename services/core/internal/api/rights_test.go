package api

import (
	"context"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

const withdrawCamera = domain.KindConsentPrefix + domain.PurposeCamera + domain.KindWithdrawnSuffix

// A candidate can turn the camera off mid-exam. From then on nothing is
// stored under the camera's consent, even from the same batch; the answers
// keep landing; a resumed runner is told not to start it again; and the
// reviewer reads it as the candidate's choice, not as a camera failure.
func TestCameraConsentCanBeWithdrawnMidExam(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff, cand, att := h.sitting("Withdraw")
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "w",
		Events: []*v1.AttemptEvent{
			{Kind: domain.KindFaceMissing, Values: []string{"4000"}},
			{Kind: withdrawCamera},
			{Kind: domain.KindFaceMissing, Values: []string{"40000"}},
			{Kind: domain.KindVisionStopped, Detail: "camera_ended"},
			{Kind: withdrawCamera},
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
		}})))
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "later",
		Events: []*v1.AttemptEvent{{Kind: domain.KindFacesMultiple, Values: []string{"9000", "2"}}, {Kind: domain.KindPasted, ItemId: "q3", Values: []string{"5"}}}})))

	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg
	var kinds []string
	for _, ev := range log.GetEvents() {
		kinds = append(kinds, ev.GetKind())
	}
	at := slices.Index(kinds, withdrawCamera)
	if at < 0 || slices.Index(kinds[at+1:], withdrawCamera) >= 0 {
		t.Fatalf("the withdrawal must be recorded once, got %q", kinds)
	}
	for _, k := range kinds[at+1:] {
		if domain.PurposeOf(k) == domain.PurposeCamera {
			t.Fatalf("%s was stored after the camera was turned off: %q", k, kinds)
		}
	}
	if !slices.Contains(kinds[:at], domain.KindFaceMissing) {
		t.Fatal("what the camera measured before it was turned off stays on the record")
	}
	if !slices.Contains(kinds, "response.changed") || !slices.Contains(kinds, domain.KindPasted) {
		t.Fatal("answers and other signals must keep landing after the camera is off")
	}
	var withdrawn *v1.Flag
	for _, f := range log.GetFlags() {
		switch f.GetType() {
		case domain.FlagWithdrawn:
			withdrawn = f
		case domain.FlagCameraStopped, domain.FlagFacesMultiple:
			t.Fatalf("turning the camera off must not read as %s: %+v", f.GetType(), f)
		}
	}
	if withdrawn == nil || withdrawn.GetSeverity() != domain.SeverityReview || !strings.Contains(withdrawn.GetReason(), "withdrew their consent to the camera") ||
		!strings.Contains(withdrawn.GetReasonId(), "menarik persetujuan untuk kamera") {
		t.Fatalf("want a consent_withdrawn flag for review, got %+v", log.GetFlags())
	}

	again := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	if !slices.Equal(again.GetWithdrawn(), []string{domain.PurposeCamera}) {
		t.Fatalf("a resumed attempt must say the camera stays off, got %q", again.GetWithdrawn())
	}
	if again := get(cand.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg.GetAttempt(); !slices.Equal(again.GetWithdrawn(), []string{domain.PurposeCamera}) {
		t.Fatalf("GetSession: withdrawn = %q", again.GetWithdrawn())
	}
}

// Withdrawing a consent never given, or something that is not a consent,
// stores nothing or is refused.
func TestOnlyAGivenConsentCanBeWithdrawn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Quiz withdraw")
	f := h.publishedFixture(staff)
	e := get(staff.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: f.exam.GetId()}))).Msg.GetExam()
	e.Tier = 1
	get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_IN_PLACE, ExpectedUpdatedAt: e.GetUpdatedAt()})))
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "w",
		Events: []*v1.AttemptEvent{{Kind: withdrawCamera}}})))
	for _, ev := range get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg.GetEvents() {
		if ev.GetKind() == withdrawCamera {
			t.Fatal("a tier 1 attempt never consented to the camera, so there is nothing to withdraw")
		}
	}
	for _, k := range []string{"consent.retina.withdrawn", "consent.camera.given", "consent."} {
		_, err := cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "bad-" + k,
			Events: []*v1.AttemptEvent{{Kind: k}}}))
		if code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v", k, err)
		}
	}
}

// The candidate reads their whole record: every event, the seal check and,
// after hand-in, the flags and scores, but not which staff member scored them.
func TestCandidateReadsTheirWholeRecord(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff, cand, att := h.sitting("Record")
	if _, err := h.browser().candidate.GetRecord(ctx, connect.NewRequest(&v1.GetRecordRequest{})); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("without the session cookie: want Unauthenticated, got %v", err)
	}
	during := get(cand.candidate.GetRecord(ctx, connect.NewRequest(&v1.GetRecordRequest{}))).Msg
	if !during.GetChainIntact() || len(during.GetAttempt().GetEvents()) == 0 || len(during.GetAttempt().GetFlagReasons()) != 0 {
		t.Fatalf("during the exam: the events, checked, and no flags yet; got %+v", during)
	}
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "p",
		Events: []*v1.AttemptEvent{{Kind: domain.KindPasted, ItemId: "q4", Values: []string{"400"}}}})))
	get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
	get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 3, Note: "Cukup."})))

	rec := get(cand.candidate.GetRecord(ctx, connect.NewRequest(&v1.GetRecordRequest{}))).Msg
	staffLog := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg
	if !rec.GetChainIntact() || len(rec.GetAttempt().GetEvents()) != len(staffLog.GetEvents()) {
		t.Fatalf("the candidate must see every event staff see: %d vs %d", len(rec.GetAttempt().GetEvents()), len(staffLog.GetEvents()))
	}
	var consents, reviews int
	for _, ev := range rec.GetAttempt().GetEvents() {
		if strings.HasPrefix(ev.GetKind(), domain.KindConsentPrefix) {
			consents++
		}
		if ev.GetKind() == domain.KindReviewScored {
			reviews++
			if ev.GetText() != "" || ev.GetValues()[0] != "3" || ev.GetDetail() != "Cukup." {
				t.Fatalf("a review event shows the score and note, not who gave it: %+v", ev)
			}
		}
	}
	if consents != len(allConsents) || reviews != 1 {
		t.Fatalf("consents %d, reviews %d", consents, reviews)
	}
	if r := rec.GetAttempt().GetFlagReasons(); len(r) == 0 || !strings.Contains(strings.Join(r, " "), "400 karakter") {
		t.Fatalf("after hand-in the record carries each flag's reason, got %q", r)
	}
	if len(rec.GetAttempt().GetReviews()) != 1 {
		t.Fatalf("reviews: %+v", rec.GetAttempt().GetReviews())
	}
	// Staff still see who scored it.
	for _, ev := range staffLog.GetEvents() {
		if ev.GetKind() == domain.KindReviewScored && !strings.HasPrefix(ev.GetText(), "staff:") {
			t.Fatal("staff's own log must keep the reviewer")
		}
	}
}
