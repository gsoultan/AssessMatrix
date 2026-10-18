package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

type authService struct{ *Server }

type staffRef struct {
	tenant db.TenantID
	id     string
}

func (s *authService) staffByEmail(ctx context.Context, email string) (*staffRef, error) {
	var r staffRef
	err := s.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT tenant_id, staff_id FROM am_staff_by_email($1)`, email).Scan(&r.tenant, &r.id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

// RequestStaffCode answers identically for known and unknown addresses, so
// it cannot be used to discover who has an account.
func (s *authService) RequestStaffCode(ctx context.Context, req *connect.Request[v1.RequestStaffCodeRequest]) (*connect.Response[v1.RequestStaffCodeResponse], error) {
	email := strings.TrimSpace(req.Msg.GetEmail())
	if !s.ipLimit.Allow(clientIP(req.Peer(), req.Header())) || !s.emailLimit.Allow(strings.ToLower(email)) {
		return nil, errSlowDown
	}
	res := connect.NewResponse(&v1.RequestStaffCodeResponse{})
	if len(email) > 320 || !strings.Contains(email, "@") {
		return res, nil
	}
	ref, err := s.staffByEmail(ctx, email)
	if err != nil {
		return nil, s.internal(ctx, "RequestStaffCode", err)
	}
	if ref == nil {
		return res, nil
	}
	now := s.now()
	err = s.DB.InTenant(ctx, ref.tenant, func(tx pgx.Tx) error {
		var sentAt *time.Time
		err := tx.QueryRow(ctx, `SELECT sent_at FROM staff_codes WHERE staff_id = $1 FOR UPDATE`, ref.id).Scan(&sentAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if sentAt != nil && now.Sub(*sentAt) < OTPCooldown {
			return nil // within the cooldown: quietly do nothing
		}
		code := secure.Code()
		if _, err := tx.Exec(ctx, `INSERT INTO staff_codes (staff_id, tenant_id, code_hash, expires_at, tries, sent_at)
			VALUES ($1, am_tenant(), $2, $3, 0, $4)
			ON CONFLICT (staff_id) DO UPDATE SET code_hash = excluded.code_hash, expires_at = excluded.expires_at,
				tries = 0, sent_at = excluded.sent_at`, ref.id, s.Keys.CodeHash("staff:"+ref.id, code), now.Add(OTPTTL), now); err != nil {
			return err
		}
		return s.queueMail(ctx, tx, "staff-code", email, code+" adalah kode masuk AssessMatrix Anda",
			"Kode masuk Anda: "+code+"\n\nBerlaku 10 menit. Jika Anda tidak meminta kode ini, abaikan email ini.")
	})
	if err != nil {
		return nil, s.internal(ctx, "RequestStaffCode", err)
	}
	return res, nil
}

func (s *authService) VerifyStaffCode(ctx context.Context, req *connect.Request[v1.VerifyStaffCodeRequest]) (*connect.Response[v1.VerifyStaffCodeResponse], error) {
	email := strings.TrimSpace(req.Msg.GetEmail())
	if !s.ipLimit.Allow(clientIP(req.Peer(), req.Header())) {
		return nil, errSlowDown
	}
	out := &v1.VerifyStaffCodeResponse{}
	ref, err := s.staffByEmail(ctx, email)
	if err != nil {
		return nil, s.internal(ctx, "VerifyStaffCode", err)
	}
	if ref == nil {
		return connect.NewResponse(out), nil
	}
	now := s.now()
	var session string
	err = s.DB.InTenant(ctx, ref.tenant, func(tx pgx.Tx) error {
		var hash []byte
		var expires time.Time
		var tries int32
		err := tx.QueryRow(ctx, `SELECT code_hash, expires_at, tries FROM staff_codes WHERE staff_id = $1 FOR UPDATE`, ref.id).
			Scan(&hash, &expires, &tries)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !expires.After(now) || tries >= OTPTries {
			return nil
		}
		code := digits(req.Msg.GetCode())
		if len(code) != 6 || !secure.Equal(hash, s.Keys.CodeHash("staff:"+ref.id, code)) {
			_, err := tx.Exec(ctx, `UPDATE staff_codes SET tries = tries + 1 WHERE staff_id = $1`, ref.id)
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM staff_codes WHERE staff_id = $1`, ref.id); err != nil {
			return err
		}
		session = secure.Token()
		if _, err := tx.Exec(ctx, `INSERT INTO staff_sessions (tenant_id, staff_id, token_hash, expires_at)
			VALUES (am_tenant(), $1, $2, $3)`, ref.id, s.Keys.SessionHash(session), now.Add(auth.StaffSessionTTL)); err != nil {
			return err
		}
		out.Ok = true
		out.Staff, err = staffProto(ctx, tx, ref.id)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "VerifyStaffCode", err)
	}
	res := connect.NewResponse(out)
	if session != "" {
		s.Auth.SetCookie(res.Header(), auth.StaffCookie, session, auth.StaffSessionTTL)
	}
	return res, nil
}

func staffProto(ctx context.Context, tx pgx.Tx, id string) (*v1.Staff, error) {
	var st v1.Staff
	var role string
	err := tx.QueryRow(ctx, `SELECT s.id, s.email, s.name, t.id, t.name, s.role FROM staff s JOIN tenants t ON t.id = s.tenant_id
		WHERE s.id = $1`, id).Scan(&st.Id, &st.Email, &st.Name, &st.TenantId, &st.TenantName, &role)
	st.Role = roleProto(role)
	return &st, err
}

func roleProto(r string) v1.Role {
	switch r {
	case auth.RoleAdmin:
		return v1.Role_ROLE_ADMIN
	case auth.RoleMember:
		return v1.Role_ROLE_MEMBER
	}
	return v1.Role_ROLE_UNSPECIFIED
}

func (s *authService) WhoAmI(ctx context.Context, _ *connect.Request[v1.WhoAmIRequest]) (*connect.Response[v1.WhoAmIResponse], error) {
	var st *v1.Staff
	err := s.tx(ctx, func(tx pgx.Tx) (err error) {
		st, err = staffProto(ctx, tx, auth.From(ctx).StaffID)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "WhoAmI", err)
	}
	return connect.NewResponse(&v1.WhoAmIResponse{Staff: st}), nil
}

func (s *authService) SignOut(ctx context.Context, req *connect.Request[v1.SignOutRequest]) (*connect.Response[v1.SignOutResponse], error) {
	token := auth.CookieValue(req.Header(), auth.StaffCookie)
	if secure.ValidToken(token) {
		hash := s.Keys.SessionHash(token)
		var tenant db.TenantID
		err := s.DB.Unscoped(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT tenant_id FROM am_staff_session($1)`, hash).Scan(&tenant)
		})
		if err == nil {
			err = s.DB.InTenant(ctx, tenant, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE staff_sessions SET revoked_at = now() WHERE token_hash = $1`, hash)
				return err
			})
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, s.internal(ctx, "SignOut", err)
		}
	}
	res := connect.NewResponse(&v1.SignOutResponse{})
	s.Auth.ClearCookie(res.Header(), auth.StaffCookie)
	return res, nil
}
