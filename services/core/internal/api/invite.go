package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
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
}

const assignmentColumns = `id, exam_id, person_id, exam_version, assigned_at, due_at, started_at, submitted_at,
	status, score_pct, passed, token_hash, sent_at, expires_at, redeemed_at, revoked_at, session_hash,
	otp_hash, otp_expires_at, otp_sent_at, otp_tries, otp_sends, self_serves`

func scanAssignment(row pgx.Row) (*assignmentRow, error) {
	var a assignmentRow
	var status int16
	var score *int16
	err := row.Scan(&a.ID, &a.ExamID, &a.PersonID, &a.ExamVersion, &a.AssignedAt, &a.DueAt, &a.StartedAt,
		&a.SubmittedAt, &status, &score, &a.Passed, &a.TokenHash, &a.SentAt, &a.ExpiresAt, &a.RedeemedAt,
		&a.RevokedAt, &a.SessionHash, &a.OTPHash, &a.OTPExpiresAt, &a.OTPSentAt, &a.OTPTries, &a.OTPSends,
		&a.SelfServes)
	if err != nil {
		return nil, err
	}
	a.Status = v1.AssignStatus(status)
	if score != nil {
		v := int32(*score)
		a.ScorePct = &v
	}
	return &a, nil
}

func getAssignment(ctx context.Context, tx pgx.Tx, id string, lock bool) (*assignmentRow, error) {
	if !validUUID(id) {
		return nil, notFound("assignment")
	}
	q := `SELECT ` + assignmentColumns + ` FROM assignments WHERE id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	a, err := scanAssignment(tx.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("assignment")
	}
	return a, err
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

func ptrMs(t *time.Time) int64 { return msPtr(t) }

func (a *assignmentRow) proto(now time.Time, history []*v1.InviteEvent) *v1.Assignment {
	out := &v1.Assignment{
		Id: a.ID, ExamId: a.ExamID, ExamVersion: a.ExamVersion, PersonId: a.PersonID,
		AssignedAt: a.AssignedAt.UnixMilli(), DueAt: ptrMs(a.DueAt), Status: a.Status,
		StartedAt: ptrMs(a.StartedAt), SubmittedAt: ptrMs(a.SubmittedAt), ScorePct: a.ScorePct, Passed: a.Passed,
		SentAt: ptrMs(a.SentAt), ExpiresAt: ptrMs(a.ExpiresAt), RedeemedAt: ptrMs(a.RedeemedAt),
		RevokedAt: ptrMs(a.RevokedAt), LinkState: a.linkState(now), OtpSends: a.OTPSends,
		SelfServes: a.SelfServes, History: history,
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
func logInvite(ctx context.Context, tx pgx.Tx, assignmentID, kind, detail string) error {
	tag, err := tx.Exec(ctx, `UPDATE invite_events SET count = count + 1, at = now()
		WHERE assignment_id = $1 AND kind = $2 AND detail = $3
		  AND seq = (SELECT max(seq) FROM invite_events WHERE assignment_id = $1)`, assignmentID, kind, detail)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO invite_events (tenant_id, assignment_id, seq, kind, detail)
		SELECT am_tenant(), $1, coalesce(max(seq), 0) + 1, $2, $3 FROM invite_events WHERE assignment_id = $1
		HAVING coalesce(max(seq), 0) < $4`, assignmentID, kind, detail, MaxHistoryLen)
	return err
}

func histories(ctx context.Context, tx pgx.Tx, ids []string) (map[string][]*v1.InviteEvent, error) {
	out := map[string][]*v1.InviteEvent{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT assignment_id, seq, at, kind, detail, count FROM invite_events
		WHERE assignment_id = ANY($1) ORDER BY assignment_id, seq`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ev v1.InviteEvent
		var at time.Time
		if err := rows.Scan(&id, &ev.Seq, &at, &ev.Kind, &ev.Detail, &ev.Count); err != nil {
			return nil, err
		}
		ev.At = at.UnixMilli()
		out[id] = append(out[id], &ev)
	}
	return out, rows.Err()
}

// retire moves the current link to the retired list, keeping the newest few.
func retire(ctx context.Context, tx pgx.Tx, a *assignmentRow) error {
	if a.TokenHash == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO retired_tokens (token_hash, tenant_id, assignment_id)
		VALUES ($1, am_tenant(), $2) ON CONFLICT DO NOTHING`, a.TokenHash, a.ID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM retired_tokens WHERE assignment_id = $1 AND token_hash NOT IN (
		SELECT token_hash FROM retired_tokens WHERE assignment_id = $1 ORDER BY retired_at DESC LIMIT $2)`, a.ID, RetiredKept)
	return err
}

type mailParts struct {
	to, name, title, issuer string
}

func mailFor(ctx context.Context, tx pgx.Tx, a *assignmentRow) (*mailParts, error) {
	var m mailParts
	err := tx.QueryRow(ctx, `SELECT p.email, p.name, e.title, e.issuer FROM people p, exams e
		WHERE p.id = $1 AND e.id = $2`, a.PersonID, a.ExamID).Scan(&m.to, &m.name, &m.title, &m.issuer)
	return &m, err
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
func (s *Server) mintAndMail(ctx context.Context, tx pgx.Tx, a *assignmentRow, kind, detail string, selfServe bool, now time.Time) error {
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
	if _, err := tx.Exec(ctx, `UPDATE assignments SET token_hash = $2, sent_at = $3, expires_at = $4,
		redeemed_at = NULL, revoked_at = NULL, session_hash = NULL, otp_hash = NULL, otp_expires_at = NULL,
		otp_sent_at = NULL, otp_tries = 0, otp_sends = 0, self_serves = $5 WHERE id = $1`,
		a.ID, s.Keys.LinkHash(token), now, expires, selfServes); err != nil {
		return err
	}
	if err := logInvite(ctx, tx, a.ID, kind, detail); err != nil {
		return err
	}
	m, err := mailFor(ctx, tx, a)
	if err != nil {
		return err
	}
	link := s.inviteURL(token)
	subject := "Undangan: " + m.title
	if again {
		subject = "Tautan baru: " + m.title
	}
	lines := []string{
		"Halo " + m.name + ",",
		"",
		m.issuer + " mengundang Anda mengerjakan “" + m.title + "”.",
		"",
		link,
		"",
		"Tautan ini hanya untuk Anda dan berlaku sampai " + tanggal(expires) + ". Saat membukanya kami mengirim kode 6 digit ke alamat ini untuk memastikan itu memang Anda.",
	}
	if again {
		lines = append(lines, "Tautan sebelumnya tidak berlaku lagi.")
	}
	lines = append(lines, "Jangan teruskan email ini.")
	return s.queueMail(ctx, tx, "invite", m.to, subject, strings.Join(lines, "\n"))
}
