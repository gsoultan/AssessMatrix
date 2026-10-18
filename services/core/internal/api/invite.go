package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/storm/runtime"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/assignment"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/exam"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/inviteevent"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/person"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/retiredtoken"
)

// The invitation rules, from docs/08-invite-links.md. Same numbers as the
// prototype's invite.ts, now enforced inside a transaction.
const (
	LinkTTL       = 7 * 24 * time.Hour
	OTPTTL        = 10 * time.Minute
	OTPTries      = 5
	OTPCooldown   = time.Minute
	OTPMaxSends   = 5
	SelfServeMax  = 5
	RetiredKept   = 5
	CandidateTTL  = 7 * 24 * time.Hour
	MaxHistoryLen = 500
)

type assignmentRow struct {
	ID, ExamID, PersonID           string
	ExamVersion                    int32
	AssignedAt                     time.Time
	DueAt, StartedAt, SubmittedAt  *time.Time
	Status                         v1.AssignStatus
	ScorePct                       *int32
	Passed                         *bool
	TokenHash                      []byte
	SentAt, ExpiresAt              *time.Time
	RedeemedAt, RevokedAt          *time.Time
	SessionHash, OTPHash           []byte
	OTPExpiresAt, OTPSentAt        *time.Time
	OTPTries, OTPSends, SelfServes int32
	Sitting                        int32
}

// assignmentOf is a stored assignment as the API reasons about it.
func assignmentOf(r assignment.Row) *assignmentRow {
	a := &assignmentRow{
		ID: idText(r.ID), ExamID: idText(r.ExamID), PersonID: idText(r.PersonID), ExamVersion: r.ExamVersion,
		AssignedAt: r.AssignedAt, DueAt: optTime(r.DueAt), StartedAt: optTime(r.StartedAt),
		SubmittedAt: optTime(r.SubmittedAt), Status: v1.AssignStatus(r.Status), Passed: opt(r.Passed),
		TokenHash: r.TokenHash, SentAt: optTime(r.SentAt), ExpiresAt: optTime(r.ExpiresAt),
		RedeemedAt: optTime(r.RedeemedAt), RevokedAt: optTime(r.RevokedAt), SessionHash: r.SessionHash,
		OTPHash: r.OtpHash, OTPExpiresAt: optTime(r.OtpExpiresAt), OTPSentAt: optTime(r.OtpSentAt),
		OTPTries: r.OtpTries, OTPSends: r.OtpSends, SelfServes: r.SelfServes, Sitting: r.Sitting,
	}
	if v, ok := r.ScorePct.Get(); ok {
		score := int32(v)
		a.ScorePct = &score
	}
	return a
}

func getAssignment(ctx context.Context, tx *db.Tx, id string, lock bool) (*assignmentRow, error) {
	if !validUUID(id) {
		return nil, notFound("assignment")
	}
	q := assignment.New().IDEq(key(id))
	if lock {
		q = q.ForUpdate()
	}
	r, ok, err := q.One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("assignment")
	}
	return assignmentOf(r), nil
}

func (a *assignmentRow) codeUsable(now time.Time) bool {
	return a.OTPHash != nil && a.OTPExpiresAt != nil && a.OTPExpiresAt.After(now) && a.OTPTries < OTPTries
}

// linkState is the order of precedence in invite.ts linkState().
func (a *assignmentRow) linkState(now time.Time) v1.LinkState {
	switch {
	case a.RevokedAt != nil:
		return v1.LinkState_LINK_STATE_REVOKED
	case a.TokenHash == nil:
		return v1.LinkState_LINK_STATE_UNSENT
	case a.RedeemedAt != nil:
		return v1.LinkState_LINK_STATE_USED
	case a.ExpiresAt == nil || !a.ExpiresAt.After(now):
		return v1.LinkState_LINK_STATE_EXPIRED
	case a.OTPSends >= OTPMaxSends && !a.codeUsable(now):
		return v1.LinkState_LINK_STATE_LOCKED
	}
	return v1.LinkState_LINK_STATE_LIVE
}

// closed: this sitting can no longer be sat, so the person may be given
// another. Handed in, or a link that ran out before anyone used it. A revoked
// or unsent invitation is still open: resend it rather than add a sitting.
func (a *assignmentRow) closed(now time.Time) bool {
	if a.Status == v1.AssignStatus_ASSIGN_STATUS_SUBMITTED {
		return true
	}
	return a.Status == v1.AssignStatus_ASSIGN_STATUS_INVITED && a.linkState(now) == v1.LinkState_LINK_STATE_EXPIRED
}

func ptrMs(t *time.Time) int64 { return msPtr(t) }

func (a *assignmentRow) proto(now time.Time, history []*v1.InviteEvent) *v1.Assignment {
	out := &v1.Assignment{
		Id: a.ID, ExamId: a.ExamID, ExamVersion: a.ExamVersion, PersonId: a.PersonID,
		AssignedAt: a.AssignedAt.UnixMilli(), DueAt: ptrMs(a.DueAt), Status: a.Status,
		StartedAt: ptrMs(a.StartedAt), SubmittedAt: ptrMs(a.SubmittedAt), ScorePct: a.ScorePct, Passed: a.Passed,
		SentAt: ptrMs(a.SentAt), ExpiresAt: ptrMs(a.ExpiresAt), RedeemedAt: ptrMs(a.RedeemedAt),
		RevokedAt: ptrMs(a.RevokedAt), LinkState: a.linkState(now), OtpSends: a.OTPSends,
		SelfServes: a.SelfServes, History: history, Sitting: a.Sitting,
	}
	return out
}

// expiryFor: the end of the due date, or a week from sending.
func expiryFor(a *assignmentRow, now time.Time) time.Time {
	if a.DueAt != nil {
		return a.DueAt.Add(24*time.Hour - time.Millisecond)
	}
	return now.Add(LinkTTL)
}

// logInvite appends to the invitation history, folding an exact repeat of
// the last event into its count so attacker-driven repeats cannot grow it.
func logInvite(ctx context.Context, tx *db.Tx, assignmentID, kind, detail string) error {
	id := key(assignmentID)
	last, ok, err := inviteevent.New().AssignmentIDEq(id).Order(inviteevent.Seq.Desc()).One(ctx, tx)
	if err != nil {
		return err
	}
	if ok && last.Kind == kind && last.Detail == detail {
		m := inviteevent.MutateKey(id, last.Seq)
		m.IncCount()
		m.SetAtNow()
		return m.Update(ctx, tx)
	}
	if last.Seq >= MaxHistoryLen {
		return nil
	}
	n := inviteevent.Create()
	n.SetTenantID(tx.Tenant())
	n.SetAssignmentID(id)
	n.SetSeq(last.Seq + 1)
	n.SetKind(kind)
	n.SetDetail(detail)
	_, err = n.Insert(ctx, tx)
	return err
}

func histories(ctx context.Context, tx *db.Tx, ids []string) (map[string][]*v1.InviteEvent, error) {
	out := map[string][]*v1.InviteEvent{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := inviteevent.New().Where(inviteevent.AssignmentID.In(keys(ids)...)).
		Order(inviteevent.AssignmentID.Asc(), inviteevent.Seq.Asc()).
		Limit(int64(len(ids))*MaxHistoryLen).All(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		id := idText(r.AssignmentID)
		out[id] = append(out[id], &v1.InviteEvent{
			Seq: r.Seq, At: r.At.UnixMilli(), Kind: r.Kind, Detail: r.Detail, Count: r.Count,
		})
	}
	return out, nil
}

// retire moves the current link to the retired list, keeping the newest few.
func retire(ctx context.Context, tx *db.Tx, a *assignmentRow) error {
	if a.TokenHash == nil {
		return nil
	}
	id := key(a.ID)
	n := retiredtoken.Create()
	n.SetTokenHash(a.TokenHash)
	n.SetTenantID(tx.Tenant())
	n.SetAssignmentID(id)
	if _, err := n.DoNothing().Insert(ctx, tx); err != nil && !errors.Is(err, runtime.ErrConflict) {
		return err
	}
	old, err := retiredtoken.New().AssignmentIDEq(id).
		Order(retiredtoken.RetiredAt.Desc(), retiredtoken.TokenHash.Desc()).
		Offset(RetiredKept).Limit(maxRetiredSweep).All(ctx, tx, nil)
	if err != nil {
		return err
	}
	for _, r := range old {
		if err := retiredtoken.Delete(ctx, tx, r.TokenHash); err != nil {
			return err
		}
	}
	return nil
}

// maxRetiredSweep bounds one retire's clean-up. Each retire adds one token
// and trims back to RetiredKept, so more than one is past the newest few
// only after a failure left extras behind.
const maxRetiredSweep = 100

type mailParts struct {
	to, name, title, issuer string
}

func mailFor(ctx context.Context, tx *db.Tx, a *assignmentRow) (*mailParts, error) {
	p, ok, err := person.New().IDEq(key(a.PersonID)).One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("person")
	}
	e, ok, err := exam.New().IDEq(key(a.ExamID)).One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("instrument")
	}
	return &mailParts{to: p.Email, name: p.Name, title: e.Title, issuer: e.Issuer}, nil
}

var jakarta = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return l
	}
	return time.FixedZone("WIB", 7*3600)
}()

var monthsID = [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}

func tanggal(t time.Time) string {
	t = t.In(jakarta)
	return fmt.Sprintf("%d %s %d %02d.%02d WIB", t.Day(), monthsID[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

// inviteURL opens the candidate runner, never the studio: a candidate's
// browser loads only the runner's code.
func (s *Server) inviteURL(token string) string {
	base := s.RunnerURL
	if base == "" {
		base = s.PublicURL
	}
	return strings.TrimRight(base, "/") + "/take/#/i/" + token
}

// mintAndMail issues a fresh link for a, retiring the old one and its
// session, and queues the email in the same transaction.
func (s *Server) mintAndMail(ctx context.Context, tx *db.Tx, a *assignmentRow, kind, detail string, selfServe bool, now time.Time) error {
	if err := retire(ctx, tx, a); err != nil {
		return err
	}
	token := secure.Token()
	again := a.SentAt != nil
	expires := expiryFor(a, now)
	if selfServe {
		// Asking for a new link never extends the deadline.
		expires = *a.ExpiresAt
	}
	selfServes := int32(0)
	if selfServe {
		selfServes = a.SelfServes + 1
	}
	m := assignment.MutateKey(key(a.ID))
	m.SetTokenHash(s.Keys.LinkHash(token))
	m.SetSentAt(now)
	m.SetExpiresAt(expires)
	m.SetRedeemedAtNull()
	m.SetRevokedAtNull()
	m.SetSessionHash(nil)
	m.SetOtpHash(nil)
	m.SetOtpExpiresAtNull()
	m.SetOtpSentAtNull()
	m.SetOtpTries(0)
	m.SetOtpSends(0)
	m.SetSelfServes(selfServes)
	if err := m.Update(ctx, tx); err != nil {
		return err
	}
	if err := logInvite(ctx, tx, a.ID, kind, detail); err != nil {
		return err
	}
	// The link itself never goes in a webhook: it opens the candidate's paper.
	if err := s.emit(ctx, tx, EventAssignmentInvited, a.PersonID, map[string]any{
		"assignment_id": a.ID, "exam_id": a.ExamID, "exam_version": a.ExamVersion, "person_id": a.PersonID,
		"reason": kind, "expires_at": expires.UTC().Format(time.RFC3339),
	}); err != nil {
		return err
	}
	mp, err := mailFor(ctx, tx, a)
	if err != nil {
		return err
	}
	link := s.inviteURL(token)
	subject := "Undangan: " + mp.title
	if again {
		subject = "Tautan baru: " + mp.title
	}
	lines := []string{
		"Halo " + mp.name + ",",
		"",
		mp.issuer + " mengundang Anda mengerjakan “" + mp.title + "”.",
		"",
		link,
		"",
		"Tautan ini hanya untuk Anda dan berlaku sampai " + tanggal(expires) + ". Saat membukanya kami mengirim kode 6 digit ke alamat ini untuk memastikan itu memang Anda.",
	}
	if again {
		lines = append(lines, "Tautan sebelumnya tidak berlaku lagi.")
	}
	lines = append(lines, "Jangan teruskan email ini.")
	return s.queueMail(ctx, tx, "invite", mp.to, subject, strings.Join(lines, "\n"))
}
