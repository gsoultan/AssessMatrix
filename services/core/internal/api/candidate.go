package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
	"unicode"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

type candidateService struct{ *Server }

// TrustProxy makes the limiter key on X-Forwarded-For. Only set it behind a
// proxy that overwrites the header.
var TrustProxy bool

func clientIP(peer connect.Peer, h interface{ Get(string) string }) string {
	if TrustProxy {
		if xff := h.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(peer.Addr)
	if err != nil {
		return peer.Addr
	}
	return host
}

var errSlowDown = connect.NewError(connect.CodeResourceExhausted, errors.New("too many requests; wait a minute"))

func (s *candidateService) limited(peer connect.Peer, h interface{ Get(string) string }) error {
	if !s.ipLimit.Allow(clientIP(peer, h)) {
		return errSlowDown
	}
	return nil
}

// link is a resolved invitation token.
type link struct {
	tenant  db.TenantID
	id      string
	current bool
}

// resolve finds the assignment a token belongs to, in any tenant, without
// revealing anything about one it does not.
func (s *candidateService) resolve(ctx context.Context, token string) (*link, error) {
	if !secure.ValidToken(token) {
		return nil, nil
	}
	var l link
	err := s.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT tenant_id, assignment_id, current FROM am_assignment_by_token($1)`,
			s.Keys.LinkHash(token)).Scan(&l.tenant, &l.id, &l.current)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// checked is an assignment that passed (or failed) the link checks.
type checked struct {
	a       *assignmentRow
	exam    *v1.Exam
	email   string
	refusal v1.Refusal
}

// check is invite.ts check(): the order of the refusals is the contract.
func (s *candidateService) check(ctx context.Context, tx pgx.Tx, l *link) (*checked, error) {
	a, err := getAssignment(ctx, tx, l.id, true)
	if err != nil {
		return nil, err
	}
	c := &checked{a: a}
	var examStatus int16
	if err := tx.QueryRow(ctx, `SELECT p.email, e.status FROM people p, exams e WHERE p.id = $1 AND e.id = $2`,
		a.PersonID, a.ExamID).Scan(&c.email, &examStatus); err != nil {
		return nil, err
	}
	now := s.now()
	switch {
	case a.RevokedAt != nil:
		c.refusal = v1.Refusal_REFUSAL_REVOKED
	case !l.current:
		c.refusal = v1.Refusal_REFUSAL_SUPERSEDED
	case v1.Status(examStatus) != v1.Status_STATUS_PUBLISHED:
		c.refusal = v1.Refusal_REFUSAL_CLOSED
	case a.Status == v1.AssignStatus_ASSIGN_STATUS_SUBMITTED:
		c.refusal = v1.Refusal_REFUSAL_SUBMITTED
	default:
		switch a.linkState(now) {
		case v1.LinkState_LINK_STATE_USED:
			c.refusal = v1.Refusal_REFUSAL_USED
		case v1.LinkState_LINK_STATE_EXPIRED:
			c.refusal = v1.Refusal_REFUSAL_EXPIRED
		case v1.LinkState_LINK_STATE_LOCKED:
			c.refusal = v1.Refusal_REFUSAL_LOCKED
		}
	}
	if c.refusal == v1.Refusal_REFUSAL_UNSPECIFIED {
		if c.exam, err = examAt(ctx, tx, a.ExamID, a.ExamVersion); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func refusalWord(r v1.Refusal) string {
	return strings.ToLower(strings.TrimPrefix(r.String(), "REFUSAL_"))
}

func summary(e *v1.Exam, a *assignmentRow, email string) *v1.InvitationSummary {
	return &v1.InvitationSummary{
		ExamTitle: e.GetTitle(), Issuer: e.GetIssuer(), Kind: e.GetKind(), Tier: e.GetTier(),
		Minutes: domain.TotalMinutes(e), PassPct: e.GetPassPct(), ItemCount: int32(len(domain.AllItems(e))),
		MaskedEmail: secure.MaskEmail(email), ExpiresAt: ptrMs(a.ExpiresAt),
	}
}

func (s *candidateService) OpenLink(ctx context.Context, req *connect.Request[v1.OpenLinkRequest]) (*connect.Response[v1.OpenLinkResponse], error) {
	if err := s.limited(req.Peer(), req.Header()); err != nil {
		return nil, err
	}
	out := &v1.OpenLinkResponse{Refusal: v1.Refusal_REFUSAL_UNKNOWN}
	l, err := s.resolve(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, s.internal(ctx, "OpenLink", err)
	}
	if l == nil {
		return connect.NewResponse(out), nil
	}
	err = s.DB.InTenant(ctx, l.tenant, func(tx pgx.Tx) error {
		c, err := s.check(ctx, tx, l)
		if err != nil {
			return err
		}
		out.MaskedEmail = secure.MaskEmail(c.email)
		if c.refusal != v1.Refusal_REFUSAL_UNSPECIFIED {
			out.Refusal = c.refusal
			return logInvite(ctx, tx, c.a.ID, "link.refused", refusalWord(c.refusal))
		}
		out.Ok, out.Refusal, out.Invitation = true, v1.Refusal_REFUSAL_UNSPECIFIED, summary(c.exam, c.a, c.email)
		return logInvite(ctx, tx, c.a.ID, "link.opened", "")
	})
	if err != nil {
		return nil, s.internal(ctx, "OpenLink", err)
	}
	return connect.NewResponse(out), nil
}

func (s *candidateService) RequestCode(ctx context.Context, req *connect.Request[v1.RequestCodeRequest]) (*connect.Response[v1.RequestCodeResponse], error) {
	if err := s.limited(req.Peer(), req.Header()); err != nil {
		return nil, err
	}
	out := &v1.RequestCodeResponse{Refusal: v1.Refusal_REFUSAL_UNKNOWN}
	l, err := s.resolve(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, s.internal(ctx, "RequestCode", err)
	}
	if l == nil {
		return connect.NewResponse(out), nil
	}
	err = s.DB.InTenant(ctx, l.tenant, func(tx pgx.Tx) error {
		c, err := s.check(ctx, tx, l)
		if err != nil {
			return err
		}
		if c.refusal != v1.Refusal_REFUSAL_UNSPECIFIED {
			out.Refusal = c.refusal
			return nil
		}
		a, now := c.a, s.now()
		if a.OTPSentAt != nil && now.Sub(*a.OTPSentAt) < OTPCooldown {
			out.Refusal, out.RetryAt = v1.Refusal_REFUSAL_COOLDOWN, a.OTPSentAt.Add(OTPCooldown).UnixMilli()
			return nil
		}
		if a.OTPSends >= OTPMaxSends {
			out.Refusal = v1.Refusal_REFUSAL_LOCKED
			return nil
		}
		code := secure.Code()
		if _, err := tx.Exec(ctx, `UPDATE assignments SET otp_hash = $2, otp_expires_at = $3, otp_tries = 0,
			otp_sent_at = $4, otp_sends = otp_sends + 1 WHERE id = $1`,
			a.ID, s.Keys.CodeHash(a.ID, code), now.Add(OTPTTL), now); err != nil {
			return err
		}
		if err := logInvite(ctx, tx, a.ID, "code.sent", "to "+secure.MaskEmail(c.email)); err != nil {
			return err
		}
		body := "Kode verifikasi Anda: " + code + "\n\nBerlaku 10 menit. Jangan berikan kode ini kepada siapa pun — kami tidak akan pernah memintanya."
		if err := s.queueMail(ctx, tx, "code", c.email, code+" adalah kode verifikasi Anda", body); err != nil {
			return err
		}
		out.Ok, out.Refusal = true, v1.Refusal_REFUSAL_UNSPECIFIED
		out.RetryAt, out.Left = now.Add(OTPCooldown).UnixMilli(), OTPMaxSends-a.OTPSends-1
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "RequestCode", err)
	}
	return connect.NewResponse(out), nil
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) && r < 128 {
			return r
		}
		return -1
	}, s)
}

func (s *candidateService) VerifyCode(ctx context.Context, req *connect.Request[v1.VerifyCodeRequest]) (*connect.Response[v1.VerifyCodeResponse], error) {
	if err := s.limited(req.Peer(), req.Header()); err != nil {
		return nil, err
	}
	out := &v1.VerifyCodeResponse{Refusal: v1.Refusal_REFUSAL_UNKNOWN}
	l, err := s.resolve(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, s.internal(ctx, "VerifyCode", err)
	}
	if l == nil {
		return connect.NewResponse(out), nil
	}
	var session string
	err = s.DB.InTenant(ctx, l.tenant, func(tx pgx.Tx) error {
		c, err := s.check(ctx, tx, l)
		if err != nil {
			return err
		}
		if c.refusal != v1.Refusal_REFUSAL_UNSPECIFIED {
			out.Refusal = c.refusal
			return nil
		}
		a, now := c.a, s.now()
		if !a.codeUsable(now) {
			out.Refusal = v1.Refusal_REFUSAL_STALE_CODE
			return nil
		}
		code := digits(req.Msg.GetCode())
		if len(code) != 6 || !secure.Equal(a.OTPHash, s.Keys.CodeHash(a.ID, code)) {
			// A wrong guess costs a try whether or not it was close.
			a.OTPTries++
			if _, err := tx.Exec(ctx, `UPDATE assignments SET otp_tries = $2 WHERE id = $1`, a.ID, a.OTPTries); err != nil {
				return err
			}
			if err := logInvite(ctx, tx, a.ID, "code.wrong", ""); err != nil {
				return err
			}
			if a.linkState(now) == v1.LinkState_LINK_STATE_LOCKED {
				out.Refusal = v1.Refusal_REFUSAL_LOCKED
				return nil
			}
			out.Refusal, out.TriesLeft = v1.Refusal_REFUSAL_WRONG_CODE, OTPTries-a.OTPTries
			return nil
		}
		// Spend the link and start the session in one write.
		session = secure.Token()
		if _, err := tx.Exec(ctx, `UPDATE assignments SET redeemed_at = $2, otp_hash = NULL, otp_expires_at = NULL,
			session_hash = $3 WHERE id = $1`, a.ID, now, s.Keys.SessionHash(session)); err != nil {
			return err
		}
		out.Ok, out.Refusal = true, v1.Refusal_REFUSAL_UNSPECIFIED
		return logInvite(ctx, tx, a.ID, "link.redeemed", "code verified; session started")
	})
	if err != nil {
		return nil, s.internal(ctx, "VerifyCode", err)
	}
	res := connect.NewResponse(out)
	if session != "" {
		s.Auth.SetCookie(res.Header(), auth.CandidateCookie, session, CandidateTTL)
	}
	return res, nil
}

func (s *candidateService) RequestNewLink(ctx context.Context, req *connect.Request[v1.RequestNewLinkRequest]) (*connect.Response[v1.RequestNewLinkResponse], error) {
	if err := s.limited(req.Peer(), req.Header()); err != nil {
		return nil, err
	}
	out := &v1.RequestNewLinkResponse{Refusal: v1.Refusal_REFUSAL_UNKNOWN}
	l, err := s.resolve(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, s.internal(ctx, "RequestNewLink", err)
	}
	if l == nil {
		return connect.NewResponse(out), nil
	}
	err = s.DB.InTenant(ctx, l.tenant, func(tx pgx.Tx) error {
		c, err := s.check(ctx, tx, l)
		if err != nil {
			return err
		}
		switch c.refusal {
		case v1.Refusal_REFUSAL_UNSPECIFIED:
			out.Ok, out.Refusal = true, v1.Refusal_REFUSAL_UNSPECIFIED // still good; nothing to replace
			return nil
		case v1.Refusal_REFUSAL_USED, v1.Refusal_REFUSAL_SUPERSEDED:
		default:
			out.Refusal = c.refusal
			return nil
		}
		a, now := c.a, s.now()
		switch {
		case a.ExpiresAt == nil || !a.ExpiresAt.After(now):
			out.Refusal = v1.Refusal_REFUSAL_EXPIRED
		case a.Status == v1.AssignStatus_ASSIGN_STATUS_SUBMITTED:
			out.Refusal = v1.Refusal_REFUSAL_SUBMITTED
		case a.SentAt != nil && now.Sub(*a.SentAt) < OTPCooldown:
			out.Refusal, out.RetryAt = v1.Refusal_REFUSAL_COOLDOWN, a.SentAt.Add(OTPCooldown).UnixMilli()
		case a.SelfServes >= SelfServeMax:
			out.Refusal = v1.Refusal_REFUSAL_LIMIT
		default:
			out.Ok, out.Refusal = true, v1.Refusal_REFUSAL_UNSPECIFIED
			return s.mintAndMail(ctx, tx, a, "link.reissued", "asked for by the link holder; mailed to the address on file", true, now)
		}
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "RequestNewLink", err)
	}
	return connect.NewResponse(out), nil
}

func (s *candidateService) EndSession(ctx context.Context, req *connect.Request[v1.EndSessionRequest]) (*connect.Response[v1.EndSessionResponse], error) {
	res := connect.NewResponse(&v1.EndSessionResponse{})
	s.Auth.ClearCookie(res.Header(), auth.CandidateCookie)
	return res, nil
}

// session loads the caller's assignment and the version it is pinned to.
func (s *candidateService) session(ctx context.Context, tx pgx.Tx, lock bool) (*assignmentRow, *v1.Exam, error) {
	p := auth.From(ctx)
	a, err := getAssignment(ctx, tx, p.AssignmentID, lock)
	if err != nil {
		return nil, nil, err
	}
	e, err := examAt(ctx, tx, a.ExamID, a.ExamVersion)
	if err != nil {
		return nil, nil, err
	}
	return a, e, nil
}

func (s *candidateService) GetSession(ctx context.Context, _ *connect.Request[v1.GetSessionRequest]) (*connect.Response[v1.GetSessionResponse], error) {
	out := &v1.GetSessionResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, e, err := s.session(ctx, tx, false)
		if err != nil {
			return err
		}
		var email string
		if err := tx.QueryRow(ctx, `SELECT email FROM people WHERE id = $1`, a.PersonID).Scan(&email); err != nil {
			return err
		}
		out.AssignmentId, out.Invitation, out.Exam = a.ID, summary(e, a, email), domain.ForCandidate(e)
		att, err := attemptBy(ctx, tx, "assignment_id", a.ID, false)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		events, _, _, err := s.loadEvents(ctx, tx, att)
		if err != nil {
			return err
		}
		out.Attempt, err = s.attemptProto(att, events, false)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "GetSession", err)
	}
	out.ServerNow = s.now().UnixMilli()
	return connect.NewResponse(out), nil
}

func isNotFound(err error) bool {
	var ce *connect.Error
	return errors.As(err, &ce) && ce.Code() == connect.CodeNotFound
}

func (s *candidateService) StartAttempt(ctx context.Context, req *connect.Request[v1.StartAttemptRequest]) (*connect.Response[v1.StartAttemptResponse], error) {
	var out *v1.Attempt
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, e, err := s.session(ctx, tx, true)
		if err != nil {
			return err
		}
		// Starting twice resumes the same attempt: a crashed browser or a
		// second device comes back to the saved work, never a blank paper.
		if att, err := attemptBy(ctx, tx, "assignment_id", a.ID, true); err == nil {
			events, _, _, err := s.loadEvents(ctx, tx, att)
			if err != nil {
				return err
			}
			out, err = s.attemptProto(att, events, false)
			return err
		} else if !isNotFound(err) {
			return err
		}
		if a.Status == v1.AssignStatus_ASSIGN_STATUS_SUBMITTED {
			return failedPrecondition("this invitation has already been submitted")
		}
		// Each purpose the tier collects under is agreed to separately, before
		// the start; nothing is collected under a purpose not agreed to.
		given := map[string]bool{}
		for _, c := range req.Msg.GetConsents() {
			given[c] = true
		}
		required := domain.RequiredConsents(e.GetTier())
		var missing []string
		for _, p := range required {
			if !given[p] {
				missing = append(missing, p)
			}
		}
		if len(missing) > 0 {
			return failedPrecondition("this assessment needs your consent first: " + strings.Join(missing, ", "))
		}
		now := s.now()
		var deadline *time.Time
		if mins := domain.TotalMinutes(e); e.GetTimed() && mins > 0 {
			d := now.Add(time.Duration(mins) * time.Minute)
			deadline = &d
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO attempts (tenant_id, assignment_id, exam_id, exam_version, person_id,
			started_at, deadline_at, last_hash) VALUES (am_tenant(), $1, $2, $3, $4, $5, $6, '\x00') RETURNING id`,
			a.ID, a.ExamID, a.ExamVersion, a.PersonID, now, deadline).Scan(&id); err != nil {
			return err
		}
		att := &attemptRow{ID: id, AssignmentID: a.ID, ExamID: a.ExamID, PersonID: a.PersonID, ExamVersion: a.ExamVersion,
			StartedAt: now, DeadlineAt: deadline, LastHash: s.genesis(id)}
		if err := s.appendEvents(ctx, tx, att, []*v1.AttemptEvent{
			{Kind: domain.KindAttemptStarted, ClientTime: now.UnixMilli(), Detail: fmt.Sprintf("Tier %d", e.GetTier())},
			// The evidence should say how we came to believe who this is.
			{Kind: domain.KindIdentityVerified, ClientTime: now.UnixMilli(), Detail: "personal link + code sent to the address on file"},
		}); err != nil {
			return err
		}
		consents := make([]*v1.AttemptEvent, 0, len(required))
		for _, p := range required {
			consents = append(consents, &v1.AttemptEvent{Kind: domain.KindConsentPrefix + p, ClientTime: now.UnixMilli(),
				Detail: "agreed on the consent screen before starting"})
		}
		if err := s.appendEvents(ctx, tx, att, consents); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE assignments SET status = $2, started_at = coalesce(started_at, $3) WHERE id = $1`,
			a.ID, int16(v1.AssignStatus_ASSIGN_STATUS_STARTED), now); err != nil {
			return err
		}
		out, err = s.attemptProto(att, nil, false)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "StartAttempt", err)
	}
	return connect.NewResponse(&v1.StartAttemptResponse{Attempt: out, ServerNow: s.now().UnixMilli()}), nil
}

// ownAttempt locks the caller's attempt, refusing any other.
func (s *candidateService) ownAttempt(ctx context.Context, tx pgx.Tx, id string) (*attemptRow, error) {
	att, err := attemptBy(ctx, tx, "id", id, true)
	if err != nil {
		return nil, err
	}
	if att.AssignmentID != auth.From(ctx).AssignmentID {
		return nil, notFound("attempt")
	}
	return att, nil
}

func validateEvent(ev *v1.AttemptEvent) error {
	k := ev.GetKind()
	switch {
	case k == "" || len(k) > MaxKindLen:
		return errors.New("event kind is missing or too long")
	case domain.ServerOnly[k]:
		return fmt.Errorf("event kind %q is written by the server only", k)
	case len(ev.GetItemId()) > domain.MaxIDLen:
		return errors.New("item id too long")
	case len(ev.GetValues()) > MaxValuesPerEvent:
		return errors.New("too many values in one event")
	case len(ev.GetText()) > domain.MaxText:
		return errors.New("answer text too long")
	case len(ev.GetDetail()) > MaxDetailLen:
		return errors.New("event detail too long")
	}
	for _, v := range ev.GetValues() {
		if len(v) > domain.MaxIDLen {
			return errors.New("value too long")
		}
	}
	// A candidate's attachment is uploaded here or inline, never a link out:
	// an outside URL would let a candidate make staff browsers fetch it.
	if f := ev.GetFile(); f != nil && (len(f.GetUrl()) > domain.MaxMediaURL || !domain.MediaURL(f.GetUrl()) || strings.HasPrefix(f.GetUrl(), "https://")) {
		return errors.New("attachment must be an image under the size limit")
	}
	return nil
}

func (s *candidateService) AppendEvents(ctx context.Context, req *connect.Request[v1.AppendEventsRequest]) (*connect.Response[v1.AppendEventsResponse], error) {
	m := req.Msg
	if len(m.GetEvents()) > MaxBatch {
		return nil, invalidArg(fmt.Errorf("at most %d events per batch", MaxBatch))
	}
	if b := m.GetBatchId(); b == "" || len(b) > 64 {
		return nil, invalidArg(errors.New("batch_id is required, up to 64 characters"))
	}
	for _, ev := range m.GetEvents() {
		if err := validateEvent(ev); err != nil {
			return nil, invalidArg(err)
		}
	}
	out := &v1.AppendEventsResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		att, err := s.ownAttempt(ctx, tx, m.GetAttemptId())
		if err != nil {
			return err
		}
		out.DeadlineAt = msPtr(att.DeadlineAt)
		var seen int64
		err = tx.QueryRow(ctx, `SELECT last_seq FROM attempt_batches WHERE attempt_id = $1 AND batch_id = $2`, att.ID, m.GetBatchId()).Scan(&seen)
		if err == nil {
			out.LastSeq = seen // a retry of a batch we already hold
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if att.SubmittedAt != nil {
			return failedPrecondition("this attempt has been submitted")
		}
		if att.LastSeq+int64(len(m.GetEvents())) > MaxEventsPerTry {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("this attempt has reached its event limit"))
		}
		keep, err := s.consented(ctx, tx, att.ID, m.GetEvents())
		if err != nil {
			return err
		}
		evs := make([]*v1.AttemptEvent, 0, len(m.GetEvents()))
		for _, ev := range m.GetEvents() {
			if !keep(ev) {
				continue
			}
			evs = append(evs, &v1.AttemptEvent{
				ClientTime: ev.GetClientTime(), Kind: ev.GetKind(), ItemId: ev.GetItemId(),
				Values: ev.GetValues(), Text: ev.GetText(), File: ev.GetFile(), Detail: ev.GetDetail(),
			})
		}
		if err := s.appendEvents(ctx, tx, att, evs); err != nil {
			return err
		}
		out.LastSeq = att.LastSeq
		_, err = tx.Exec(ctx, `INSERT INTO attempt_batches (tenant_id, attempt_id, batch_id, last_seq) VALUES (am_tenant(), $1, $2, $3)`,
			att.ID, m.GetBatchId(), att.LastSeq)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "AppendEvents", err)
	}
	out.ServerNow = s.now().UnixMilli()
	return connect.NewResponse(out), nil
}

// consented decides which events of a batch may be stored: a signal collected
// under a purpose the candidate did not agree to (or that the tier does not
// collect) is dropped, never stored. It is dropped rather than refused because
// the batch also carries answers, and a refused batch would block them in the
// candidate's queue for good. Only a batch with signals costs the lookup.
func (s *candidateService) consented(ctx context.Context, tx pgx.Tx, attemptID string, evs []*v1.AttemptEvent) (func(*v1.AttemptEvent) bool, error) {
	all := func(*v1.AttemptEvent) bool { return true }
	if !slices.ContainsFunc(evs, func(ev *v1.AttemptEvent) bool { return domain.PurposeOf(ev.GetKind()) != "" }) {
		return all, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT kind FROM attempt_events WHERE attempt_id = $1 AND kind LIKE 'consent.%'`, attemptID)
	if err != nil {
		return nil, err
	}
	kinds, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	return func(ev *v1.AttemptEvent) bool {
		p := domain.PurposeOf(ev.GetKind())
		return p == "" || slices.Contains(kinds, domain.KindConsentPrefix+p)
	}, nil
}

// ExpirySkew is how early a forced submit is honoured, for clock drift.
const ExpirySkew = 5 * time.Second

func (s *candidateService) SubmitAttempt(ctx context.Context, req *connect.Request[v1.SubmitAttemptRequest]) (*connect.Response[v1.SubmitAttemptResponse], error) {
	out := &v1.SubmitAttemptResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		att, err := s.ownAttempt(ctx, tx, req.Msg.GetAttemptId())
		if err != nil {
			return err
		}
		events, _, _, err := s.loadEvents(ctx, tx, att)
		if err != nil {
			return err
		}
		if att.SubmittedAt != nil { // idempotent: a retried submit sees the result
			out.Ok = true
			out.Attempt, err = s.attemptProto(att, events, false)
			return err
		}
		e, err := examAt(ctx, tx, att.ExamID, att.ExamVersion)
		if err != nil {
			return err
		}
		now := s.now()
		// Force is only for a clock that ran out, and the server's clock decides.
		expired := att.DeadlineAt != nil && !now.Before(att.DeadlineAt.Add(-ExpirySkew))
		force := req.Msg.GetForce() && expired
		answers, _ := domain.Fold(events, att.deadline())
		if !force {
			if missing := domain.Unanswered(e, answers); len(missing) > 0 {
				out.MissingItemIds = missing
				return nil
			}
		}
		survey := e.GetKind() == v1.Kind_KIND_SURVEY
		var score *v1.Score
		var scoreJSON []byte
		detail := "response recorded"
		if !survey {
			score = domain.Score(e, answers)
			if scoreJSON, err = protojson.Marshal(score); err != nil {
				return err
			}
			detail = fmt.Sprintf("%d%%", score.GetPct())
		}
		kind := domain.KindAttemptSubmitted
		if force {
			kind = domain.KindAttemptExpired
		}
		if err := s.appendEvents(ctx, tx, att, []*v1.AttemptEvent{{Kind: kind, ClientTime: now.UnixMilli(), Detail: detail}}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE attempts SET submitted_at = $2, score = $3 WHERE id = $1`, att.ID, now, scoreJSON); err != nil {
			return err
		}
		var pct *int16
		var passed *bool
		if score != nil {
			p := int16(score.GetPct())
			pct, passed = &p, &score.Passed
		}
		if _, err := tx.Exec(ctx, `UPDATE assignments SET status = $2, submitted_at = $3, score_pct = $4, passed = $5 WHERE id = $1`,
			att.AssignmentID, int16(v1.AssignStatus_ASSIGN_STATUS_SUBMITTED), now, pct, passed); err != nil {
			return err
		}
		att.SubmittedAt, att.Score = &now, scoreJSON
		events, _, _, err = s.loadEvents(ctx, tx, att)
		if err != nil {
			return err
		}
		out.Ok = true
		out.Attempt, err = s.attemptProto(att, events, false)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "SubmitAttempt", err)
	}
	return connect.NewResponse(out), nil
}
