package api

import (
	"context"
	"slices"
	"time"

	"github.com/gsoultan/storm/runtime"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/analysisrequest"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/assignment"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/itemscore"
)

// storeScore writes an attempt's score where it is read (the attempt and its
// assignment), refreshes its per-item index for analysis, and marks the exam
// version's statistics stale. Called at hand-in and after every review, in
// the same transaction as the event that changed the score.
func storeScore(ctx context.Context, tx *db.Tx, att *attemptRow, e *v1.Exam, answers map[string]*v1.ItemAnswer, reviews map[string]domain.Review, score *v1.Score) error {
	body, err := protojson.Marshal(score)
	if err != nil {
		return err
	}
	attemptID, examID := key(att.ID), key(att.ExamID)
	ma := attempt.MutateKey(attemptID)
	ma.SetScore(body)
	if err := ma.Update(ctx, tx); err != nil {
		return err
	}
	att.Score = body
	mg := assignment.MutateKey(key(att.AssignmentID))
	mg.SetScorePct(int16(score.GetPct()))
	// No verdict (NULL) while points wait for a person.
	if score.GetPendingHuman() == 0 {
		mg.SetPassed(score.GetPassed())
	} else {
		mg.SetPassedNull()
	}
	if err := mg.Update(ctx, tx); err != nil {
		return err
	}
	// The item index is replaced whole: the old rows go and the new ones
	// arrive in one batch.
	old, err := itemscore.New().AttemptIDEq(attemptID).Limit(maxItemScores).All(ctx, tx, nil)
	if err != nil {
		return err
	}
	ops := make([]runtime.BatchOp, 0, len(old)+len(domain.AllItems(e)))
	for _, r := range old {
		ops = append(ops, itemscore.DeleteOp(r.AttemptID, r.ItemID))
	}
	for _, it := range domain.AllItems(e) {
		if domain.IsScale(it.GetType()) {
			continue
		}
		a := answers[it.GetId()]
		r := itemscore.Row{
			TenantID: tx.Tenant(), AttemptID: attemptID, ExamID: examID, ExamVersion: att.ExamVersion,
			ItemID: it.GetId(), MaxPoints: it.GetPoints(),
		}
		if p, ok := itemPoints(it, a, reviews); ok {
			r.Points = some(p)
		}
		r.Choices = slices.Clone(a.GetValues())
		if r.Choices == nil {
			r.Choices = []string{}
		}
		ops = append(ops, itemscore.InsertOp(r))
	}
	if err := tx.Batch(ctx, ops, nil); err != nil {
		return err
	}
	q := analysisrequest.Create()
	q.SetTenantID(tx.Tenant())
	q.SetExamID(examID)
	q.SetExamVersion(att.ExamVersion)
	q.SetRequestedAt(time.Now())
	_, err = q.OnConflictTenantIDExamIDExamVersion().Insert(ctx, tx)
	return err
}

// itemPoints is what one scored item earned. ok is false for an essay still
// waiting for a person, which has no points yet.
func itemPoints(it *v1.Item, a *v1.ItemAnswer, reviews map[string]domain.Review) (points int32, ok bool) {
	if it.GetType() == v1.ItemType_ITEM_TYPE_ESSAY {
		rv, ok := reviews[it.GetId()]
		return min(max(rv.Points, 0), it.GetPoints()), ok
	}
	return domain.Earned(it, a), true
}

// itemsData is the per-question part of an attempt event: each scored
// question's points out of its own maximum, in the instrument's order.
// Points are null while an essay waits for review. Scales are left out.
// Kept terse: 500 questions with long ids must fit in one event.
func itemsData(e *v1.Exam, answers map[string]*v1.ItemAnswer, reviews map[string]domain.Review) []map[string]any {
	out := []map[string]any{}
	for _, it := range domain.AllItems(e) {
		if domain.IsScale(it.GetType()) {
			continue
		}
		d := map[string]any{"item_id": it.GetId(), "points": nil, "max": it.GetPoints()}
		if p, ok := itemPoints(it, answers[it.GetId()], reviews); ok {
			d["points"] = p
		}
		// Only when a wrong answer cost something, so the items still add up
		// to the score's points.
		if pen := domain.Penalty(it, answers[it.GetId()]); pen > 0 {
			d["penalty"] = pen
		}
		out = append(out, d)
	}
	return out
}

// maxItemScores bounds the read of an attempt's item index: well past the
// number of items any instrument may hold.
const maxItemScores = 10_000
