package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

type assignmentService struct{ *Server }

const maxAssignAtOnce = 5000

func (s *assignmentService) list(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]*v1.Assignment, error) {
	rows, err := tx.Query(ctx, `SELECT `+assignmentColumns+` FROM assignments `+where+` ORDER BY assigned_at LIMIT 50000`, args...)
	if err != nil {
		return nil, err
	}
	var as []*assignmentRow
	var ids []string
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		as = append(as, a)
		ids = append(ids, a.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hist, err := histories(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]*v1.Assignment, 0, len(as))
	for _, a := range as {
		out = append(out, a.proto(now, hist[a.ID]))
	}
	return out, nil
}

func (s *assignmentService) ListAssignments(ctx context.Context, req *connect.Request[v1.ListAssignmentsRequest]) (*connect.Response[v1.ListAssignmentsResponse], error) {
	var out []*v1.Assignment
	err := s.tx(ctx, func(tx pgx.Tx) (err error) {
		if id := req.Msg.GetExamId(); id != "" {
			if !validUUID(id) {
				return notFound("instrument")
			}
			out, err = s.list(ctx, tx, `WHERE exam_id = $1`, id)
			return err
		}
		out, err = s.list(ctx, tx, ``)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "ListAssignments", err)
	}
	return connect.NewResponse(&v1.ListAssignmentsResponse{Assignments: out}), nil
}

func (s *assignmentService) AssignPeople(ctx context.Context, req *connect.Request[v1.AssignPeopleRequest]) (*connect.Response[v1.AssignPeopleResponse], error) {
	m := req.Msg
	if len(m.GetPersonIds()) > maxAssignAtOnce {
		return nil, invalidArg(errors.New("at most 5,000 people at once"))
	}
	for _, id := range m.GetPersonIds() {
		if !validUUID(id) {
			return nil, notFound("person")
		}
	}
	var due *time.Time
	if m.GetDueAt() > 0 {
		t := time.UnixMilli(m.GetDueAt())
		due = &t
	}
	var out v1.AssignPeopleResponse
	err := s.tx(ctx, func(tx pgx.Tx) error {
		e, err := getExam(ctx, tx, m.GetExamId(), false)
		if err != nil {
			return err
		}
		// An invitation opens the live version, or the working one for a draft.
		version := e.GetPublishedVersion()
		if version == 0 {
			version = e.GetVersion()
		}
		var fresh []string
		for _, pid := range m.GetPersonIds() {
			var id string
			// The person must exist in this tenant; RLS makes another tenant's
			// person invisible, so the FK alone cannot be relied on to say so.
			err := tx.QueryRow(ctx, `INSERT INTO assignments (tenant_id, exam_id, exam_version, person_id, due_at)
				SELECT am_tenant(), $1, $2, p.id, $4 FROM people p WHERE p.id = $3
				ON CONFLICT (exam_id, person_id) DO NOTHING RETURNING id`, e.GetId(), version, pid, due).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1)`, pid).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return notFound("person")
				}
				continue // already assigned: a no-op
			}
			if err != nil {
				return err
			}
			fresh = append(fresh, id)
		}
		if e.GetStatus() == v1.Status_STATUS_PUBLISHED {
			n, err := s.send(ctx, tx, fresh)
			if err != nil {
				return err
			}
			out.Sent = n
		}
		if out.Assignments, err = s.list(ctx, tx, `WHERE id = ANY($1)`, fresh); err != nil {
			return err
		}
		return audit(ctx, tx, "assignments.created", e.GetId(), map[string]any{"count": len(fresh), "sent": out.Sent})
	})
	if err != nil {
		return nil, s.internal(ctx, "AssignPeople", err)
	}
	return connect.NewResponse(&out), nil
}

// send mints and mails a link for each assignment that can be opened:
// not submitted, and on a published instrument.
func (s *assignmentService) send(ctx context.Context, tx pgx.Tx, ids []string) (int32, error) {
	now := s.now()
	var n int32
	for _, id := range ids {
		a, err := getAssignment(ctx, tx, id, true)
		if err != nil {
			return n, err
		}
		var status int16
		if err := tx.QueryRow(ctx, `SELECT status FROM exams WHERE id = $1`, a.ExamID).Scan(&status); err != nil {
			return n, err
		}
		if a.Status == v1.AssignStatus_ASSIGN_STATUS_SUBMITTED || v1.Status(status) != v1.Status_STATUS_PUBLISHED {
			continue
		}
		kind := "link.sent"
		if a.SentAt != nil {
			kind = "link.resent"
		}
		if err := s.mintAndMail(ctx, tx, a, kind, "by an administrator", false, now); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *assignmentService) SendInvites(ctx context.Context, req *connect.Request[v1.SendInvitesRequest]) (*connect.Response[v1.SendInvitesResponse], error) {
	ids := req.Msg.GetAssignmentIds()
	if len(ids) > maxAssignAtOnce {
		return nil, invalidArg(errors.New("at most 5,000 invitations at once"))
	}
	var sent int32
	err := s.tx(ctx, func(tx pgx.Tx) (err error) {
		if sent, err = s.send(ctx, tx, ids); err != nil {
			return err
		}
		return audit(ctx, tx, "invites.sent", "", map[string]any{"requested": len(ids), "sent": sent})
	})
	if err != nil {
		return nil, s.internal(ctx, "SendInvites", err)
	}
	return connect.NewResponse(&v1.SendInvitesResponse{Sent: sent}), nil
}

func (s *assignmentService) RevokeInvite(ctx context.Context, req *connect.Request[v1.RevokeInviteRequest]) (*connect.Response[v1.RevokeInviteResponse], error) {
	var out *v1.Assignment
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, err := getAssignment(ctx, tx, req.Msg.GetAssignmentId(), true)
		if err != nil {
			return err
		}
		if err := retire(ctx, tx, a); err != nil {
			return err
		}
		// Kills the link and the session it started. The row and history stay.
		if _, err := tx.Exec(ctx, `UPDATE assignments SET revoked_at = now(), token_hash = NULL, session_hash = NULL,
			otp_hash = NULL, otp_expires_at = NULL WHERE id = $1`, a.ID); err != nil {
			return err
		}
		if err := logInvite(ctx, tx, a.ID, "link.revoked", "by an administrator"); err != nil {
			return err
		}
		list, err := s.list(ctx, tx, `WHERE id = $1`, a.ID)
		if err != nil {
			return err
		}
		out = list[0]
		return audit(ctx, tx, "invite.revoked", a.ID, nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "RevokeInvite", err)
	}
	return connect.NewResponse(&v1.RevokeInviteResponse{Assignment: out}), nil
}

func (s *assignmentService) Unassign(ctx context.Context, req *connect.Request[v1.UnassignRequest]) (*connect.Response[v1.UnassignResponse], error) {
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, err := getAssignment(ctx, tx, req.Msg.GetId(), true)
		if err != nil {
			return err
		}
		var sat bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM attempts WHERE assignment_id = $1)`, a.ID).Scan(&sat); err != nil {
			return err
		}
		if sat {
			return failedPrecondition("this invitation has an attempt; revoke it instead")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM assignments WHERE id = $1`, a.ID); err != nil {
			return err
		}
		return audit(ctx, tx, "assignment.deleted", a.ID, nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "Unassign", err)
	}
	return connect.NewResponse(&v1.UnassignResponse{}), nil
}
