// Package auth decides who is calling and whether they may call this
// procedure. Authentication (which cookie, which row) and authorization (the
// per-procedure policy) are separate steps, and an unknown procedure is
// refused: a new RPC with no policy entry cannot be reached at all.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// Access is who may call a procedure.
type Access int

const (
	// Public procedures check their own credential (a link token, an email).
	Public Access = iota + 1
	Staff
	Candidate
	// Admin is a staff member whose role in the session's tenant is admin.
	Admin
)

// Roles a staff member can hold in a tenant, as stored.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

const (
	StaffCookie     = "am_staff"
	CandidateCookie = "am_candidate"
	StaffSessionTTL = 12 * time.Hour
)

// Principal is an authenticated caller. Its tenant is the only tenant any of
// its queries may touch.
type Principal struct {
	Tenant       db.TenantID
	StaffID      string
	AssignmentID string
	// Role is the staff member's role in Tenant. Empty for a candidate.
	Role string
}

type ctxKey struct{}

func with(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the caller. Handlers behind Staff or Candidate always have one.
func From(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// Authenticator resolves cookies to principals.
type Authenticator struct {
	DB     *db.DB
	Keys   *secure.Keys
	Policy map[string]Access
	// Secure cookies. Off only for local http development.
	SecureCookies bool
}

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

// ErrNotAdmin refuses a member an administrator's procedure.
var ErrNotAdmin = connect.NewError(connect.CodePermissionDenied, errors.New("only an administrator of this tenant can do this"))

// Interceptor enforces the policy on every unary call.
func (a *Authenticator) Interceptor() connect.Interceptor { return interceptor{a} }

// interceptor guards unary and server-streaming procedures alike. A
// UnaryInterceptorFunc alone would let every streaming procedure through
// unchecked.
type interceptor struct{ a *Authenticator }

func (i interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := i.a.authorize(ctx, req.Spec().Procedure, req.Header())
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (i interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := i.a.authorize(ctx, conn.Spec().Procedure, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (i interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// authorize resolves the caller for a procedure's access policy, refusing
// any procedure that has none.
func (a *Authenticator) authorize(ctx context.Context, procedure string, h http.Header) (context.Context, error) {
	access, ok := a.Policy[procedure]
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("no access policy for %s", procedure))
	}
	switch access {
	case Public:
		return ctx, nil
	case Staff:
		p, err := a.staff(ctx, h)
		if err != nil {
			return nil, err
		}
		return with(ctx, p), nil
	case Admin:
		p, err := a.staff(ctx, h)
		if err != nil {
			return nil, err
		}
		if p.Role != RoleAdmin {
			return nil, ErrNotAdmin
		}
		return with(ctx, p), nil
	case Candidate:
		p, err := a.candidate(ctx, h)
		if err != nil {
			return nil, err
		}
		return with(ctx, p), nil
	}
	return nil, connect.NewError(connect.CodeInternal, errors.New("unhandled access level"))
}

func cookie(h http.Header, name string) string {
	r := http.Request{Header: h}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func (a *Authenticator) staff(ctx context.Context, h http.Header) (*Principal, error) {
	token := cookie(h, StaffCookie)
	if !secure.ValidToken(token) {
		return nil, errUnauthenticated
	}
	var p Principal
	err := a.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT tenant_id, staff_id, role FROM am_staff_session($1)`, a.Keys.SessionHash(token)).
			Scan(&p.Tenant, &p.StaffID, &p.Role)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("resolve staff session: %w", err)
	}
	return &p, nil
}

func (a *Authenticator) candidate(ctx context.Context, h http.Header) (*Principal, error) {
	token := cookie(h, CandidateCookie)
	if !secure.ValidToken(token) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no invitation session"))
	}
	var p Principal
	err := a.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT tenant_id, assignment_id FROM am_assignment_by_session($1)`, a.Keys.SessionHash(token)).
			Scan(&p.Tenant, &p.AssignmentID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("this session has ended"))
	}
	if err != nil {
		return nil, fmt.Errorf("resolve candidate session: %w", err)
	}
	return &p, nil
}

// SetCookie writes an HttpOnly, SameSite=Strict session cookie.
func (a *Authenticator) SetCookie(h http.Header, name, value string, maxAge time.Duration) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	}
	if maxAge > 0 {
		c.MaxAge = int(maxAge.Seconds())
	}
	h.Add("Set-Cookie", c.String())
}

// ClearCookie removes a session cookie from the browser.
func (a *Authenticator) ClearCookie(h http.Header, name string) {
	c := &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.SecureCookies, SameSite: http.SameSiteStrictMode}
	h.Add("Set-Cookie", c.String())
}

// CookieValue reads a cookie from request headers.
func CookieValue(h http.Header, name string) string { return cookie(h, name) }

// Either resolves a plain HTTP request (not an RPC) to whoever holds a session:
// a staff member first, else a candidate. It is for endpoints both may use,
// such as images, where the row behind the URL decides the rest.
func (a *Authenticator) Either(ctx context.Context, h http.Header) (*Principal, error) {
	if p, err := a.staff(ctx, h); err == nil {
		return p, nil
	} else if !errors.Is(err, errUnauthenticated) {
		return nil, err
	}
	return a.candidate(ctx, h)
}

// With attaches a principal to a context, for handlers outside the interceptor.
func With(ctx context.Context, p *Principal) context.Context { return with(ctx, p) }
