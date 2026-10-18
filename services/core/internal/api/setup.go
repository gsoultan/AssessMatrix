package api

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// setupService is first-run setup: the super administrator, and the default
// tenant they also administer. The database, not this handler, is what makes
// it happen once: am_setup_first_tenant refuses when any tenant exists.
type setupService struct{ *Server }

var (
	regionRe     = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)?$`)
	staffEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

func (s *setupService) region() string {
	if s.Media.Region != "" {
		return s.Media.Region
	}
	return "id"
}

func (s *setupService) GetSetupStatus(ctx context.Context, _ *connect.Request[v1.GetSetupStatusRequest]) (*connect.Response[v1.GetSetupStatusResponse], error) {
	out := &v1.GetSetupStatusResponse{SchemaLatest: db.LatestMigration(), Region: s.region()}
	err := s.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT am_setup_needed(), am_schema_version()`).Scan(&out.Needed, &out.SchemaVersion)
	})
	if err != nil {
		return nil, s.internal(ctx, "GetSetupStatus", err)
	}
	if !out.Needed {
		// Once set up, anyone may still ask, so say no more than that.
		out = &v1.GetSetupStatusResponse{}
	}
	return connect.NewResponse(out), nil
}

// defaultTenant names the first tenant when setup is given no name.
const defaultTenant = "Default"

// setupDoneState is the SQLSTATE a second setup gets from the database.
const setupDoneState = "AM001"

func (s *setupService) CompleteSetup(ctx context.Context, req *connect.Request[v1.CompleteSetupRequest]) (*connect.Response[v1.CompleteSetupResponse], error) {
	if !s.ipLimit.Allow(clientIP(req.Peer(), req.Header())) {
		return nil, errSlowDown
	}
	m := req.Msg
	if !s.Keys.SetupCodeMatches(m.GetSetupCode()) {
		s.Log.WarnContext(ctx, "setup refused: wrong setup code")
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("that setup code is wrong; it is printed in the server log, or run `core setup-code`"))
	}
	org := strings.TrimSpace(m.GetOrganizationName())
	if org == "" {
		org = defaultTenant
	}
	email := strings.TrimSpace(m.GetAdminEmail())
	name := strings.TrimSpace(m.GetAdminName())
	region := strings.TrimSpace(m.GetRegion())
	if region == "" {
		region = s.region()
	}
	switch {
	case len(org) > 200:
		return nil, invalidArg(errors.New("the default tenant's name is at most 200 characters"))
	case len(email) > 320 || !staffEmailRe.MatchString(email):
		return nil, invalidArg(errors.New("the super administrator needs a valid email address"))
	case len(name) > 200:
		return nil, invalidArg(errors.New("the super administrator's name is at most 200 characters"))
	case !regionRe.MatchString(region):
		return nil, invalidArg(errors.New("the region is a two-letter code such as id"))
	}

	now := s.now()
	session := secure.Token()
	var tenant, staffID string
	var st *v1.Staff
	// One transaction: the tenant, the super administrator, the audit row and
	// their first session commit together or not at all.
	err := s.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT tenant_id, staff_id FROM am_setup_first_tenant($1, $2, $3, $4)`,
			org, region, email, name).Scan(&tenant, &staffID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_sessions (tenant_id, staff_id, token_hash, expires_at)
			VALUES (am_tenant(), $1, $2, $3)`, staffID, s.Keys.SessionHash(session), now.Add(auth.StaffSessionTTL)); err != nil {
			return err
		}
		var err error
		st, err = staffProto(ctx, tx, staffID)
		return err
	})
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == setupDoneState {
		return nil, failedPrecondition("setup is already complete; sign in instead")
	}
	if err != nil {
		return nil, s.internal(ctx, "CompleteSetup", err)
	}
	s.Log.InfoContext(ctx, "first-run setup complete", "tenant", tenant, "staff", staffID)
	res := connect.NewResponse(&v1.CompleteSetupResponse{Staff: st})
	s.Auth.SetCookie(res.Header(), auth.StaffCookie, session, auth.StaffSessionTTL)
	return res, nil
}
