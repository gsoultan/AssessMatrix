package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// MaxReviewNote bounds what a reviewer writes back to a candidate.
const MaxReviewNote = 2000

func (s *resultService) ListReviews(ctx context.Context, req *connect.Request[v1.ListReviewsRequest]) (*connect.Response[v1.ListReviewsResponse], error) {
	out := &v1.ListReviewsResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := getExam(ctx, tx, req.Msg.GetExamId(), false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+attemptColumns+` FROM attempts
			WHERE exam_id = $1 AND submitted_at IS NOT NULL ORDER BY submitted_at, id LIMIT 50000`, req.Msg.GetExamId())
		if err != nil {
			return err
		}
		atts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*attemptRow, error) { return scanAttempt(r) })
		if err != nil {
			return err
		}
		versions := map[int32]*v1.Exam{}
		for i, a := range atts {
			e, ok := versions[a.ExamVersion]
			if !ok {
				if e, err = examAt(ctx, tx, a.ExamID, a.ExamVersion); err != nil {
					return err
				}
				versions[a.ExamVersion] = e
			}
			essays := essaysOf(e)
			if len(essays) == 0 {
				continue
			}
			events, _, _, err := s.loadEvents(ctx, tx, a)
			if err != nil {
				return err
			}
			answers, _ := domain.Fold(events, a.deadline())
			reviews := domain.Reviews(events)
			number := questionNumbers(e)
			for _, it := range essays {
				r, done := reviews[it.GetId()]
				if done && !req.Msg.GetIncludeDone() {
					continue
				}
				ans := answers[it.GetId()]
				out.Tasks = append(out.Tasks, &v1.ReviewTask{
					AttemptId: a.ID, Label: "Response " + strconv.Itoa(i+1), ItemId: it.GetId(), Number: int32(number(it.GetId())),
					Stem: it.GetStem(), Text: ans.GetText(), File: ans.GetFile(), Points: it.GetPoints(),
					Reviewed: done, Given: r.Points, Note: r.Note, SubmittedAt: a.SubmittedAt.UnixMilli(),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "ListReviews", err)
	}
	return connect.NewResponse(out), nil
}

func essaysOf(e *v1.Exam) []*v1.Item {
	var out []*v1.Item
	for _, it := range domain.AllItems(e) {
		if it.GetType() == v1.ItemType_ITEM_TYPE_ESSAY {
			out = append(out, it)
		}
	}
	return out
}

func (s *resultService) ScoreAnswer(ctx context.Context, req *connect.Request[v1.ScoreAnswerRequest]) (*connect.Response[v1.ScoreAnswerResponse], error) {
	m := req.Msg
	note := strings.TrimSpace(m.GetNote())
	if utf8.RuneCountInString(note) > MaxReviewNote {
		return nil, invalidArg(fmt.Errorf("the note is longer than %d characters", MaxReviewNote))
	}
	out := &v1.ScoreAnswerResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		att, err := attemptBy(ctx, tx, "id", m.GetAttemptId(), true)
		if err != nil {
			return err
		}
		if att.SubmittedAt == nil {
			return failedPrecondition("this attempt has not been handed in yet")
		}
		e, err := examAt(ctx, tx, att.ExamID, att.ExamVersion)
		if err != nil {
			return err
		}
		var item *v1.Item
		for _, it := range essaysOf(e) {
			if it.GetId() == m.GetItemId() {
				item = it
			}
		}
		if item == nil {
			return invalidArg(errors.New("only an essay question is scored by a person"))
		}
		if m.GetPoints() < 0 || m.GetPoints() > item.GetPoints() {
			return invalidArg(fmt.Errorf("points must be between 0 and %d", item.GetPoints()))
		}
		if e.GetKind() == v1.Kind_KIND_SURVEY {
			return failedPrecondition("a questionnaire is not scored")
		}
		by := "staff:" + auth.From(ctx).StaffID
		if err := s.appendEvents(ctx, tx, att, []*v1.AttemptEvent{{
			Kind: domain.KindReviewScored, ItemId: item.GetId(), Values: []string{strconv.Itoa(int(m.GetPoints()))},
			Detail: note, Text: by, ClientTime: s.now().UnixMilli(),
		}}); err != nil {
			return err
		}
		events, _, _, err := s.loadEvents(ctx, tx, att)
		if err != nil {
			return err
		}
		answers, _ := domain.Fold(events, att.deadline())
		reviews := domain.Reviews(events)
		out.Score = domain.Score(e, answers, reviews)
		if err := storeScore(ctx, tx, att, e, answers, reviews, out.Score); err != nil {
			return err
		}
		cred, err := s.settleCredential(ctx, tx, att, e, out.Score)
		if err != nil {
			return err
		}
		if cred != "" {
			out.CredentialUrl = s.CredentialURL(cred)
		}
		return audit(ctx, tx, "attempt.reviewed", att.ID, map[string]any{"item": item.GetId(), "points": m.GetPoints()})
	})
	if err != nil {
		return nil, s.internal(ctx, "ScoreAnswer", err)
	}
	return connect.NewResponse(out), nil
}

func (s *resultService) GetItemAnalysis(ctx context.Context, req *connect.Request[v1.GetItemAnalysisRequest]) (*connect.Response[v1.GetItemAnalysisResponse], error) {
	out := &v1.GetItemAnalysisResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetExamId(), false)
		if err != nil {
			return err
		}
		var body []byte
		q := `SELECT stats FROM item_stats WHERE exam_id = $1 ORDER BY exam_version DESC LIMIT 1`
		args := []any{e.GetId()}
		if v := req.Msg.GetExamVersion(); v > 0 {
			q = `SELECT stats FROM item_stats WHERE exam_id = $1 AND exam_version = $2`
			args = append(args, v)
		}
		err = tx.QueryRow(ctx, q, args...).Scan(&body)
		if errors.Is(err, pgx.ErrNoRows) {
			out.Pending = true
			out.ExamVersion = req.Msg.GetExamVersion()
			return nil
		}
		if err != nil {
			return err
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, out); err != nil {
			return err
		}
		// Stale while newer hand-ins wait in the queue.
		var queued bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM analysis_queue WHERE exam_id = $1 AND exam_version = $2)`,
			e.GetId(), out.GetExamVersion()).Scan(&queued); err != nil {
			return err
		}
		out.Pending = queued
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "GetItemAnalysis", err)
	}
	return connect.NewResponse(out), nil
}
