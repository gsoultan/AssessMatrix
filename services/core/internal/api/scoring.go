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
	mg.SetPassed(score.GetPassed())
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
		switch {
		case it.GetType() == v1.ItemType_ITEM_TYPE_ESSAY:
			if rv, ok := reviews[it.GetId()]; ok {
				r.Points = some(min(max(rv.Points, 0), it.GetPoints()))
			}
		default:
			r.Points = some(domain.Earned(it, a))
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

// maxItemScores bounds the read of an attempt's item index: well past the
// number of items any instrument may hold.
const maxItemScores = 10_000
