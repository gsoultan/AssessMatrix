package api

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// storeScore writes an attempt's score where it is read (the attempt and its
// assignment), refreshes its per-item index for analysis, and marks the exam
// version's statistics stale. Called at hand-in and after every review, in
// the same transaction as the event that changed the score.
func storeScore(ctx context.Context, tx pgx.Tx, att *attemptRow, e *v1.Exam, answers map[string]*v1.ItemAnswer, reviews map[string]domain.Review, score *v1.Score) error {
	body, err := protojson.Marshal(score)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE attempts SET score = $2 WHERE id = $1`, att.ID, body); err != nil {
		return err
	}
	att.Score = body
	// No verdict (NULL) while points wait for a person.
	var passed *bool
	if score.GetPendingHuman() == 0 {
		p := score.GetPassed()
		passed = &p
	}
	if _, err := tx.Exec(ctx, `UPDATE assignments SET score_pct = $2, passed = $3 WHERE id = $1`,
		att.AssignmentID, int16(score.GetPct()), passed); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM item_scores WHERE attempt_id = $1`, att.ID); err != nil {
		return err
	}
	rows := [][]any{}
	for _, it := range domain.AllItems(e) {
		if domain.IsScale(it.GetType()) {
			continue
		}
		a := answers[it.GetId()]
		var points *int32
		switch {
		case it.GetType() == v1.ItemType_ITEM_TYPE_ESSAY:
			if r, ok := reviews[it.GetId()]; ok {
				p := min(max(r.Points, 0), it.GetPoints())
				points = &p
			}
		default:
			p := domain.Earned(it, a)
			points = &p
		}
		choices := slices.Clone(a.GetValues())
		if choices == nil {
			choices = []string{}
		}
		rows = append(rows, []any{att.ID, att.ExamID, att.ExamVersion, it.GetId(), points, it.GetPoints(), choices})
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `INSERT INTO item_scores (tenant_id, attempt_id, exam_id, exam_version, item_id, points, max_points, choices)
			VALUES (am_tenant(), $1, $2, $3, $4, $5, $6, $7)`, r...); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO analysis_queue (tenant_id, exam_id, exam_version) VALUES (am_tenant(), $1, $2)
		ON CONFLICT (tenant_id, exam_id, exam_version) DO UPDATE SET requested_at = now()`, att.ExamID, att.ExamVersion)
	return err
}
