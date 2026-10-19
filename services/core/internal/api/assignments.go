package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/gsoultan/storm/runtime"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/assignment"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/exam"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/person"
)

type assignmentService struct{ *Server }

const maxAssignAtOnce = 5000

func (s *assignmentService) list(ctx context.Context, tx *db.Tx, q assignment.Query) ([]*v1.Assignment, error) {
	rows, err := q.Order(assignment.AssignedAt.Asc(), assignment.ID.Asc()).Limit(50000).All(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	as := make([]*assignmentRow, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		a := assignmentOf(r)
		as = append(as, a)
		ids = append(ids, a.ID)
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
	err := s.tx(ctx, func(tx *db.Tx) (err error) {
		if id := req.Msg.GetExamId(); id != "" {
			if !validUUID(id) {
				return notFound("instrument")
			}
			out, err = s.list(ctx, tx, assignment.New().ExamIDEq(key(id)))
			return err
		}
		out, err = s.list(ctx, tx, assignment.New())
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		e, err := getExam(ctx, tx, m.GetExamId(), false)
		if err != nil {
			return err
		}
		// An invitation opens the live version, or the working one for a draft.
		version := e.GetPublishedVersion()
		if version == 0 {
			version = e.GetVersion()
		}
		// Or an earlier published one, asked for by number. Only a frozen
		// version can be sat: the working copy may still change under them.
		if want := m.GetExamVersion(); want != 0 {
			if want < 1 || want > e.GetPublishedVersion() {
				return invalidArg(errors.New("that version has not been published"))
			}
			if _, err := examAt(ctx, tx, e.GetId(), want); err != nil {
				return err
			}
			version = want
		}
		// The people must exist in this tenant. RLS makes another tenant's
		// person invisible to this read, but a foreign key is checked without
		// it, so the insert alone cannot be relied on to refuse one.
		pids := keys(m.GetPersonIds())
		found, err := person.New().Where(person.ID.In(pids...)).Limit(maxAssignAtOnce).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		here := make(map[[16]byte]bool, len(found))
		for _, p := range found {
			here[p.ID] = true
		}
		examID := key(e.GetId())
		now := s.now()
		var fresh []string
		for _, pid := range pids {
			if !here[pid] {
				return notFound("person")
			}
			// A new sitting only once the last one is closed; otherwise a no-op.
			sitting := int32(1)
			last, ok, err := assignment.New().Where(assignment.ExamID.Eq(examID), assignment.PersonID.Eq(pid)).
				Order(assignment.Sitting.Desc()).ForUpdate().One(ctx, tx)
			switch {
			case err != nil:
				return err
			case !ok:
			case !assignmentOf(last).closed(now):
				continue
			default:
				sitting = last.Sitting + 1
			}
			n := assignment.Create()
			n.SetTenantID(tx.Tenant())
			n.SetExamID(examID)
			n.SetExamVersion(version)
			n.SetPersonID(pid)
			if due != nil {
				n.SetDueAt(*due)
			}
			n.SetSitting(sitting)
			row, err := n.OnConflictExamIDPersonIDSitting().DoNothing().Insert(ctx, tx)
			if errors.Is(err, runtime.ErrConflict) {
				continue // another request added this sitting first
			}
			if err != nil {
				return err
			}
			fresh = append(fresh, idText(row.ID))
		}
		if e.GetStatus() == v1.Status_STATUS_PUBLISHED {
			n, err := s.send(ctx, tx, fresh)
			if err != nil {
				return err
			}
			out.Sent = n
		}
		if out.Assignments, err = s.list(ctx, tx, assignment.New().Where(assignment.ID.In(keys(fresh)...))); err != nil {
			return err
		}
		return audit(ctx, tx, "assignments.created", e.GetId(), map[string]any{"count": len(fresh), "sent": out.Sent, "version": version})
	})
	if err != nil {
		return nil, s.internal(ctx, "AssignPeople", err)
	}
	return connect.NewResponse(&out), nil
}

// send mints and mails a link for each assignment that can be opened:
// not submitted, and on a published instrument.
func (s *assignmentService) send(ctx context.Context, tx *db.Tx, ids []string) (int32, error) {
	now := s.now()
	var n int32
	for _, id := range ids {
		a, err := getAssignment(ctx, tx, id, true)
		if err != nil {
			return n, err
		}
		e, ok, err := exam.New().IDEq(key(a.ExamID)).One(ctx, tx)
		if err != nil {
			return n, err
		}
		if !ok {
			return n, notFound("instrument")
		}
		if a.Status == v1.AssignStatus_ASSIGN_STATUS_SUBMITTED || v1.Status(e.Status) != v1.Status_STATUS_PUBLISHED {
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
	err := s.tx(ctx, func(tx *db.Tx) (err error) {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		a, err := getAssignment(ctx, tx, req.Msg.GetAssignmentId(), true)
		if err != nil {
			return err
		}
		if err := retire(ctx, tx, a); err != nil {
			return err
		}
		// Kills the link and the session it started. The row and history stay.
		m := assignment.MutateKey(key(a.ID))
		m.SetRevokedAtNow()
		m.SetTokenHash(nil)
		m.SetSessionHash(nil)
		m.SetOtpHash(nil)
		m.SetOtpExpiresAtNull()
		if err := m.Update(ctx, tx); err != nil {
			return err
		}
		if err := logInvite(ctx, tx, a.ID, "link.revoked", "by an administrator"); err != nil {
			return err
		}
		list, err := s.list(ctx, tx, assignment.New().IDEq(key(a.ID)))
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		a, err := getAssignment(ctx, tx, req.Msg.GetId(), true)
		if err != nil {
			return err
		}
		sat, err := attempt.New().AssignmentIDEq(key(a.ID)).Exists(ctx, tx)
		if err != nil {
			return err
		}
		if sat {
			return failedPrecondition("this invitation has an attempt; revoke it instead")
		}
		if err := assignment.Delete(ctx, tx, key(a.ID)); err != nil {
			return err
		}
		return audit(ctx, tx, "assignment.deleted", a.ID, nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "Unassign", err)
	}
	return connect.NewResponse(&v1.UnassignResponse{}), nil
}
