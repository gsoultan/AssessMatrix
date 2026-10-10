package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

type resultService struct{ *Server }

func (s *resultService) ListResponses(ctx context.Context, req *connect.Request[v1.ListResponsesRequest]) (*connect.Response[v1.ListResponsesResponse], error) {
	var out []*v1.Response
	err := s.tx(ctx, func(tx pgx.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetExamId(), false)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+attemptColumns+` FROM attempts
			WHERE exam_id = $1 AND submitted_at IS NOT NULL ORDER BY submitted_at LIMIT 50000`, e.GetId())
		if err != nil {
			return err
		}
		var atts []*attemptRow
		for rows.Next() {
			a, err := scanAttempt(rows)
			if err != nil {
				rows.Close()
				return err
			}
			atts = append(atts, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, a := range atts {
			events, _, _, err := s.loadEvents(ctx, tx, a)
			if err != nil {
				return err
			}
			answers, _ := domain.Fold(events, a.deadline())
			r := &v1.Response{
				AttemptId: a.ID, AssignmentId: a.AssignmentID, PersonId: a.PersonID, ExamVersion: a.ExamVersion,
				SubmittedAt: msPtr(a.SubmittedAt), Answers: answers,
			}
			if e.GetAnonymous() {
				// Who answered stays out of an anonymous questionnaire's results.
				r.PersonId, r.AssignmentId, r.AttemptId = "", "", ""
			}
			if a.Score != nil {
				var sc v1.Score
				if err := protojson.Unmarshal(a.Score, &sc); err != nil {
					return err
				}
				r.Score = &sc
			}
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "ListResponses", err)
	}
	return connect.NewResponse(&v1.ListResponsesResponse{Responses: out}), nil
}

func (s *resultService) GetAttemptLog(ctx context.Context, req *connect.Request[v1.GetAttemptLogRequest]) (*connect.Response[v1.GetAttemptLogResponse], error) {
	out := &v1.GetAttemptLogResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, err := attemptBy(ctx, tx, "id", req.Msg.GetAttemptId(), false)
		if err != nil {
			return err
		}
		out.PurgedAt = msPtr(a.PurgedAt)
		out.Events, out.ChainIntact, out.BrokenAtSeq, err = s.loadEvents(ctx, tx, a)
		if err != nil {
			return err
		}
		e, err := examAt(ctx, tx, a.ExamID, a.ExamVersion)
		if err != nil {
			return err
		}
		out.Flags = flagsOut(domain.Flags(out.Events, questionNumbers(e), out.ChainIntact, out.BrokenAtSeq))
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "GetAttemptLog", err)
	}
	return connect.NewResponse(out), nil
}

// questionNumbers gives a question's position as the candidate saw it.
func questionNumbers(e *v1.Exam) func(string) int {
	n := map[string]int{}
	for i, it := range domain.AllItems(e) {
		n[it.GetId()] = i + 1
	}
	return func(id string) int { return n[id] }
}

func flagsOut(fs []domain.Flag) []*v1.Flag {
	out := make([]*v1.Flag, 0, len(fs))
	for _, f := range fs {
		out = append(out, &v1.Flag{Type: f.Type, Severity: f.Severity, Reason: f.Reason, ReasonId: f.ReasonID, ItemId: f.ItemID})
	}
	return out
}
