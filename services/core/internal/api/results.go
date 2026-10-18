package api

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
)

type resultService struct{ *Server }

func (s *resultService) ListResponses(ctx context.Context, req *connect.Request[v1.ListResponsesRequest]) (*connect.Response[v1.ListResponsesResponse], error) {
	var out []*v1.Response
	err := s.tx(ctx, func(tx *db.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetExamId(), false)
		if err != nil {
			return err
		}
		rows, err := attempt.New().Where(attempt.ExamID.Eq(key(e.GetId())), attempt.SubmittedAt.IsNotNull()).
			Order(attempt.SubmittedAt.Asc(), attempt.ID.Asc()).Limit(50000).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		atts := make([]*attemptRow, len(rows))
		for i, r := range rows {
			atts[i] = attemptOf(r)
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
	err := s.tx(ctx, func(tx *db.Tx) error {
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
