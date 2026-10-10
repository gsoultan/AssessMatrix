package api

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

// An essay goes to a person, blind; their score lands in the log, on the
// attempt and on the assignment, and the exam's statistics are marked stale.
func TestEssayReviewScoresTheAttempt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Review")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
		Events: []*v1.AttemptEvent{
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
			{Kind: "response.typed", ItemId: "q4", Text: "Because the slice header is nil."},
		}})))

	_, err := staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 3}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("an attempt still in progress must not be scored, got %v", err)
	}
	sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()}))).Msg.GetAttempt().GetScore()
	if sub.GetRaw() != 2 || sub.GetMax() != 4 || sub.GetPendingHuman() != 5 {
		t.Fatalf("at hand-in: %+v, want 2/4 with 5 pending", sub)
	}

	tasks := get(staff.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId()}))).Msg.GetTasks()
	if len(tasks) != 1 || tasks[0].GetLabel() != "Response 1" || tasks[0].GetItemId() != "q4" || tasks[0].GetNumber() != 4 ||
		!strings.Contains(tasks[0].GetText(), "slice header") || tasks[0].GetPoints() != 5 {
		t.Fatalf("review queue: %+v", tasks)
	}

	for _, bad := range []*v1.ScoreAnswerRequest{
		{AttemptId: att.GetId(), ItemId: "q4", Points: 6},
		{AttemptId: att.GetId(), ItemId: "q4", Points: -1},
		{AttemptId: att.GetId(), ItemId: "q1", Points: 1},
	} {
		if _, err := staff.results.ScoreAnswer(ctx, connect.NewRequest(bad)); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%+v: want InvalidArgument, got %v", bad, err)
		}
	}
	sc := get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 4, Note: "Right idea."}))).Msg.GetScore()
	if sc.GetRaw() != 6 || sc.GetMax() != 9 || sc.GetPct() != 67 || sc.GetPendingHuman() != 0 {
		t.Fatalf("after review: %+v, want 6/9 = 67%%, nothing pending", sc)
	}
	if left := get(staff.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId()}))).Msg.GetTasks(); len(left) != 0 {
		t.Fatalf("a scored essay left the queue, got %+v", left)
	}
	done := get(staff.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId(), IncludeDone: true}))).Msg.GetTasks()
	if len(done) != 1 || !done[0].GetReviewed() || done[0].GetGiven() != 4 || done[0].GetNote() != "Right idea." {
		t.Fatalf("looking again: %+v", done)
	}

	a := get(staff.assign.ListAssignments(ctx, connect.NewRequest(&v1.ListAssignmentsRequest{ExamId: f.exam.GetId()}))).Msg.GetAssignments()[0]
	if a.GetScorePct() != 67 {
		t.Fatalf("the assignment must carry the reviewed score, got %d", a.GetScorePct())
	}
	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()})))
	last := log.Msg.GetEvents()[len(log.Msg.GetEvents())-1]
	if !log.Msg.GetChainIntact() || last.GetKind() != "review.scored" || last.GetValues()[0] != "4" || !strings.HasPrefix(last.GetText(), "staff:") {
		t.Fatalf("the review must be sealed into the log with who gave it, got %+v", last)
	}

	var n int
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM item_scores WHERE attempt_id = $1 AND points IS NOT NULL`, att.GetId()).Scan(&n); err != nil || n != 4 {
		t.Fatalf("every scored item must be indexed for analysis, got %d (%v)", n, err)
	}
	an := get(staff.results.GetItemAnalysis(ctx, connect.NewRequest(&v1.GetItemAnalysisRequest{ExamId: f.exam.GetId()}))).Msg
	if !an.GetPending() {
		t.Fatal("with nothing computed yet, the analysis must say it is pending")
	}

	_, err = cand.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 5}))
	if code(err) != connect.CodeUnauthenticated && code(err) != connect.CodePermissionDenied {
		t.Fatalf("a candidate must not score their own essay, got %v", err)
	}
}
