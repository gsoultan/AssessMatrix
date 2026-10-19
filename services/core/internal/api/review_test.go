package api

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
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
	if len(tasks) != 1 || tasks[0].GetLabel() != "Answer 1" || tasks[0].GetItemId() != "q4" || tasks[0].GetNumber() != 4 ||
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

// The queue gives a reviewer nothing that leads back to a candidate: no
// attempt id, no hand-in time, numbers in an order that is not hand-in order
// and differs per question, and a sealed task as the only handle. A second
// reviewer does not see the first one's score.
func TestReviewQueueIsBlind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Blind")
	tenant := h.lastTenant
	f := h.publishedFixture(staff)
	e := get(staff.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: f.exam.GetId()}))).Msg.GetExam()
	e.Sections[0].Items = append(e.Sections[0].Items, &v1.Item{Id: "q5", Type: v1.ItemType_ITEM_TYPE_ESSAY, Stem: "Again", Points: 5})
	get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_IN_PLACE, ExpectedUpdatedAt: e.GetUpdatedAt()})))
	fixtures := []*fixture{f}
	for range 7 {
		email := "cand-" + secure.Token()[:8] + "@example.test"
		p := get(staff.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Name: "Siti", Email: email}}))).Msg.GetPerson()
		res := get(staff.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: f.exam.GetId(), PersonIds: []string{p.GetId()}})))
		fixtures = append(fixtures, &fixture{exam: f.exam, person: p, assign: res.Msg.GetAssignments()[0], email: email})
	}
	var handedIn []string
	for i, fx := range fixtures {
		cand := h.redeem(fx)
		att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
		get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
			Events: []*v1.AttemptEvent{
				{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
				{Kind: "response.typed", ItemId: "q4", Text: "four by " + strconv.Itoa(i)},
				{Kind: "response.typed", ItemId: "q5", Text: "five by " + strconv.Itoa(i)},
			}})))
		get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
		handedIn = append(handedIn, strconv.Itoa(i))
	}

	tasks := get(staff.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId()}))).Msg.GetTasks()
	if len(tasks) != 2*len(fixtures) {
		t.Fatalf("want %d tasks, got %d", 2*len(fixtures), len(tasks))
	}
	order := map[string][]string{} // question -> who wrote each answer, in queue order
	for _, task := range tasks {
		if task.GetAttemptId() != "" || task.GetSubmittedAt() != 0 || task.GetTask() == "" {
			t.Fatalf("a task must carry only its sealed handle: %+v", task)
		}
		who := task.GetText()[strings.LastIndex(task.GetText(), " ")+1:]
		order[task.GetItemId()] = append(order[task.GetItemId()], who)
		if want := "Answer " + strconv.Itoa(len(order[task.GetItemId()])); task.GetLabel() != want {
			t.Fatalf("labels count up per question: got %q, want %q", task.GetLabel(), want)
		}
	}
	if slices.Equal(order["q4"], handedIn) && slices.Equal(order["q5"], handedIn) {
		t.Fatal("the queue must not follow hand-in order")
	}
	if slices.Equal(order["q4"], order["q5"]) {
		t.Fatal("one candidate's answers must not sit at the same number on every question")
	}

	// The handle scores the answer it names, and nothing forged gets through.
	first := tasks[0]
	get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{Task: first.GetTask(), Points: 4, Note: "Good."})))
	for _, bad := range []string{"nope", first.GetTask()[:len(first.GetTask())-2] + "AA"} {
		if _, err := staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{Task: bad, Points: 1})); code(err) != connect.CodeInvalidArgument {
			t.Errorf("forged task %q: want InvalidArgument, got %v", bad, err)
		}
	}
	// Another tenant's staff cannot use a handle from this queue.
	if _, err := h.tenant("Elsewhere").results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{Task: first.GetTask(), Points: 1})); code(err) != connect.CodeNotFound {
		t.Fatalf("a handle from another tenant: want NotFound, got %v", err)
	}

	mine := get(staff.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId(), IncludeDone: true}))).Msg.GetTasks()
	if mine[0].GetText() != first.GetText() || !mine[0].GetReviewed() || mine[0].GetGiven() != 4 || mine[0].GetNote() != "Good." || mine[0].GetScoredByOther() {
		t.Fatalf("the reviewer sees their own score again, at the same number: %+v", mine[0])
	}
	second := "second-" + secure.Token()[:8] + "@example.test"
	if _, err := h.owner.Exec(ctx, `INSERT INTO staff (tenant_id, email, role) VALUES ($1, $2, 'admin')`, tenant, second); err != nil {
		t.Fatal(err)
	}
	theirs := get(h.signIn(second).results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: f.exam.GetId(), IncludeDone: true}))).Msg.GetTasks()
	if !theirs[0].GetReviewed() || !theirs[0].GetScoredByOther() || theirs[0].GetGiven() != 0 || theirs[0].GetNote() != "" {
		t.Fatalf("a second reviewer must not see the first one's score: %+v", theirs[0])
	}
}
