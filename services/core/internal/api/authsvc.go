package api

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staff"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staffcode"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staffsession"
)

type authService struct{ *Server }

type staffRef struct {
	tenant db.TenantID
	id     string
}

func (s *authService) staffByEmail(ctx context.Context, email string) (*staffRef, error) {
	var r queries.StaffTenant
	var ok bool
	err := s.DB.Unscoped(ctx, func(tx *db.Tx) (err error) {
		r, ok, err = queries.StaffByEmail.One(ctx, tx, email)
		return err
	})
	if err != nil || !ok {
		return nil, err
	}
	return &staffRef{tenant: db.TenantID(idText(r.TenantID)), id: idText(r.StaffID)}, nil
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
	err = s.DB.InTenant(ctx, ref.tenant, func(tx *db.Tx) error {
		last, sent, err := staffcode.New().StaffIDEq(key(ref.id)).ForUpdate().One(ctx, tx)
		if err != nil {
			return err
		}
		if sent && now.Sub(last.SentAt) < OTPCooldown {
			return nil // within the cooldown: quietly do nothing
		}
		code := secure.Code()
		n := staffcode.Create()
		n.SetStaffID(key(ref.id))
		n.SetTenantID(tx.Tenant())
		n.SetCodeHash(s.Keys.CodeHash("staff:"+ref.id, code))
		n.SetExpiresAt(now.Add(OTPTTL))
		n.SetTries(0)
		n.SetSentAt(now)
		if _, err := n.OnConflictStaffID().Insert(ctx, tx); err != nil {
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
	err = s.DB.InTenant(ctx, ref.tenant, func(tx *db.Tx) error {
		sc, ok, err := staffcode.New().StaffIDEq(key(ref.id)).ForUpdate().One(ctx, tx)
		if err != nil || !ok {
			return err
		}
		if !sc.ExpiresAt.After(now) || sc.Tries >= OTPTries {
			return nil
		}
		code := digits(req.Msg.GetCode())
		if len(code) != 6 || !secure.Equal(sc.CodeHash, s.Keys.CodeHash("staff:"+ref.id, code)) {
			m := staffcode.Mutate(sc)
			m.IncTries()
			return m.Update(ctx, tx)
		}
		if err := staffcode.Delete(ctx, tx, sc.StaffID); err != nil {
			return err
		}
		session = secure.Token()
		n := staffsession.Create()
		n.SetTenantID(tx.Tenant())
		n.SetStaffID(sc.StaffID)
		n.SetTokenHash(s.Keys.SessionHash(session))
		n.SetExpiresAt(now.Add(auth.StaffSessionTTL))
		if _, err := n.Insert(ctx, tx); err != nil {
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

func staffProto(ctx context.Context, tx *db.Tx, id string) (*v1.Staff, error) {
	rows, err := staff.New().IDEq(key(id)).Limit(1).AllWithTenant(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, notFound("staff member")
	}
	r := rows[0]
	super, _, err := queries.SuperAdmin.One(ctx, tx, r.Email)
	if err != nil {
		return nil, err
	}
	return &v1.Staff{
		Id: idText(r.ID), Email: r.Email, Name: r.Name, TenantId: idText(r.TenantID), TenantName: r.TenantName,
		Role: roleProto(r.Role), SuperAdmin: super.Yes,
	}, nil
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
	err := s.tx(ctx, func(tx *db.Tx) (err error) {
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
		var sess queries.StaffSessionRow
		var live bool
		err := s.DB.Unscoped(ctx, func(tx *db.Tx) (err error) {
			sess, live, err = queries.StaffSession.One(ctx, tx, hash)
			return err
		})
		if err == nil && live {
			err = s.DB.InTenant(ctx, db.TenantID(idText(sess.TenantID)), func(tx *db.Tx) error {
				r, ok, err := staffsession.New().Where(staffsession.TokenHash.Eq(hash)).One(ctx, tx)
				if err != nil || !ok {
					return err
				}
				m := staffsession.Mutate(r)
				m.SetRevokedAtNow()
				return m.Update(ctx, tx)
			})
		}
		if err != nil {
			return nil, s.internal(ctx, "SignOut", err)
		}
	}
	res := connect.NewResponse(&v1.SignOutResponse{})
	s.Auth.ClearCookie(res.Header(), auth.StaffCookie)
	return res, nil
}
