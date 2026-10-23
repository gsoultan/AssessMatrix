package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

// The scoring rules an author sets on a question survive the save and decide
// the score: a wrong answer costs its penalty, a half-right multiple answer
// earns half, and the analysis index never sees a negative item.
func TestPartialCreditAndPenaltyReachTheScore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Rules")
	f := h.publishedFixtureWith(staff, func(e *v1.Exam) {
		items := e.GetSections()[0].GetItems()
		items[0].Penalty = 1
		items[1].Points, items[1].PartialCredit = 2, true
	})
	saved := f.exam.GetSections()[0].GetItems()
	if saved[0].GetPenalty() != 1 || !saved[1].GetPartialCredit() {
		t.Fatalf("the scoring rules were lost on save: %+v %+v", saved[0], saved[1])
	}

	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b1", Events: []*v1.AttemptEvent{
		{Kind: "response.changed", ItemId: "q1", Values: []string{"a"}},
		{Kind: "response.changed", ItemId: "q2", Values: []string{"a"}},
		{Kind: "response.typed", ItemId: "q3", Text: "Jakarta"},
	}})))
	sc := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()}))).Msg.GetAttempt().GetScore()
	// q1 wrong: -1. q2 one of two keys: 1 of 2. q3 right: 1.
	if sc.GetRaw() != 1 || sc.GetMax() != 5 || sc.GetPct() != 20 {
		t.Fatalf("score = %+v, want 1 of 5, 20%%", sc)
	}

	rows, err := h.owner.Query(ctx, `SELECT item_id, points FROM item_scores WHERE attempt_id = $1 AND points IS NOT NULL ORDER BY item_id`, att.GetId())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var id string
		var p int
		if err := rows.Scan(&id, &p); err != nil {
			t.Fatal(err)
		}
		got[id] = p
	}
	if got["q1"] != 0 || got["q2"] != 1 || got["q3"] != 1 {
		t.Fatalf("item index = %v, want q1 0, q2 1, q3 1", got)
	}
}
