// Package api implements the assessmatrix.v1 Connect services.
//
// Every handler runs its queries inside db.InTenant with the tenant taken from
// the authenticated principal, or from a SECURITY DEFINER lookup of a hashed
// credential. No request field ever names a tenant.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/gsoultan/storm/runtime"

	"github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1/assessmatrixv1connect"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/limit"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/metrics"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/auditlog"
	"github.com/gsoultan/assessmatrix/services/core/internal/webhook"
)

// Server holds what every handler needs.
type Server struct {
	DB   *db.DB
	Keys *secure.Keys
	Auth *auth.Authenticator
	Jobs *river.Client[pgx.Tx]
	Log  *slog.Logger
	// PublicURL is where candidates open links, e.g. https://exam.example.id.
	PublicURL string
	// RunnerURL is the origin of the candidate runner, which a deployment may
	// put on its own host so candidate and staff cookies never share one.
	// Empty means PublicURL.
	RunnerURL string
	Now       func() time.Time
	// Media is where uploaded images go, and the one region it may hold.
	Media media.Regional
	// WebhookPolicy says which receiver URLs staff may register.
	WebhookPolicy webhook.Policy

	// Per-IP limits on the unauthenticated candidate and sign-in endpoints.
	// The per-row counters (tries, sends, cooldowns) are the real defence;
	// these stop one client hammering many rows.
	ipLimit    *limit.Limiter
	emailLimit *limit.Limiter
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Policy is who may call each procedure. The interceptor refuses anything not
// listed, and a test fails if a procedure in the schema is missing here.
func Policy() map[string]auth.Access {
	p := map[string]auth.Access{}
	set := func(a auth.Access, procs ...string) {
		for _, x := range procs {
			p[x] = a
		}
	}
	set(auth.Public,
		assessmatrixv1connect.AuthServiceRequestStaffCodeProcedure,
		assessmatrixv1connect.AuthServiceVerifyStaffCodeProcedure,
		assessmatrixv1connect.CandidateServiceOpenLinkProcedure,
		assessmatrixv1connect.CandidateServiceRequestCodeProcedure,
		assessmatrixv1connect.CandidateServiceVerifyCodeProcedure,
		assessmatrixv1connect.CandidateServiceRequestNewLinkProcedure,
		// Clearing your own cookie needs no proof of who you are.
		assessmatrixv1connect.CandidateServiceEndSessionProcedure,
		assessmatrixv1connect.AuthServiceSignOutProcedure,
		// First-run setup proves itself with the setup code, and the
		// database refuses it once an organisation exists.
		assessmatrixv1connect.SetupServiceGetSetupStatusProcedure,
		assessmatrixv1connect.SetupServiceCompleteSetupProcedure,
	)
	set(auth.Candidate,
		assessmatrixv1connect.CandidateServiceGetSessionProcedure,
		assessmatrixv1connect.CandidateServiceStartAttemptProcedure,
		assessmatrixv1connect.CandidateServiceAppendEventsProcedure,
		assessmatrixv1connect.CandidateServiceSubmitAttemptProcedure,
	)
	set(auth.Staff,
		assessmatrixv1connect.AuthServiceWhoAmIProcedure,
		assessmatrixv1connect.ExamServiceListExamsProcedure,
		assessmatrixv1connect.ExamServiceGetExamProcedure,
		assessmatrixv1connect.ExamServiceCreateExamProcedure,
		assessmatrixv1connect.ExamServiceSaveExamProcedure,
		assessmatrixv1connect.ExamServiceDuplicateExamProcedure,
		assessmatrixv1connect.ExamServiceSetExamStatusProcedure,
		assessmatrixv1connect.ExamServiceDeleteExamProcedure,
		assessmatrixv1connect.ExamServiceListExamVersionsProcedure,
		assessmatrixv1connect.PersonServiceListPeopleProcedure,
		assessmatrixv1connect.PersonServiceAddPersonProcedure,
		assessmatrixv1connect.PersonServiceEnsurePeopleProcedure,
		assessmatrixv1connect.PersonServiceDeletePersonProcedure,
		assessmatrixv1connect.PersonServiceErasePersonProcedure,
		assessmatrixv1connect.AssignmentServiceListAssignmentsProcedure,
		assessmatrixv1connect.AssignmentServiceAssignPeopleProcedure,
		assessmatrixv1connect.AssignmentServiceUnassignProcedure,
		assessmatrixv1connect.AssignmentServiceSendInvitesProcedure,
		assessmatrixv1connect.AssignmentServiceRevokeInviteProcedure,
		assessmatrixv1connect.ResultServiceListResponsesProcedure,
		assessmatrixv1connect.ResultServiceGetAttemptLogProcedure,
		assessmatrixv1connect.ResultServiceListReviewsProcedure,
		assessmatrixv1connect.ResultServiceScoreAnswerProcedure,
		assessmatrixv1connect.ResultServiceGetItemAnalysisProcedure,
		assessmatrixv1connect.ProctorServiceWatchExamProcedure,
		assessmatrixv1connect.TenantServiceListMyTenantsProcedure,
		assessmatrixv1connect.TenantServiceSwitchTenantProcedure,
	)
	// Who may work here, and what leaves this tenant for another:
	// an instrument shared out, or candidate data sent to a webhook receiver
	// (whose signing secret a member must never see).
	set(auth.Admin,
		assessmatrixv1connect.WebhookServiceListWebhooksProcedure,
		assessmatrixv1connect.WebhookServiceCreateWebhookProcedure,
		assessmatrixv1connect.WebhookServiceUpdateWebhookProcedure,
		assessmatrixv1connect.WebhookServiceRotateWebhookSecretProcedure,
		assessmatrixv1connect.WebhookServiceDeleteWebhookProcedure,
		assessmatrixv1connect.WebhookServiceListDeliveriesProcedure,
		assessmatrixv1connect.WebhookServiceRedeliverProcedure,
		assessmatrixv1connect.WebhookServicePingWebhookProcedure,
		assessmatrixv1connect.TenantServiceListAccessProcedure,
		assessmatrixv1connect.TenantServiceSetAccessProcedure,
		assessmatrixv1connect.TenantServiceListExamCopiesProcedure,
		assessmatrixv1connect.TenantServiceShareExamProcedure,
	)
	// Tenants themselves. Who administers each is checked in SetAccess, which
	// an administrator may also call for their members.
	set(auth.SuperAdmin,
		assessmatrixv1connect.TenantServiceCreateTenantProcedure,
	)
	return p
}

// Handler mounts every service with the auth interceptor in front.
func (s *Server) Handler() http.Handler {
	s.limits()
	opts := []connect.HandlerOption{
		// Metrics outermost, so refusals by the policy are counted too.
		connect.WithInterceptors(metrics.Interceptor(), s.Auth.Interceptor()),
		// An instrument with inline images is the largest legitimate request.
		connect.WithReadMaxBytes(16 << 20),
	}
	mux := http.NewServeMux()
	mux.Handle(assessmatrixv1connect.NewAuthServiceHandler(&authService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewExamServiceHandler(&examService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewPersonServiceHandler(&personService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewAssignmentServiceHandler(&assignmentService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewCandidateServiceHandler(&candidateService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewResultServiceHandler(&resultService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewProctorServiceHandler(&proctorService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewSetupServiceHandler(&setupService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewWebhookServiceHandler(&webhookService{s}, opts...))
	mux.Handle(assessmatrixv1connect.NewTenantServiceHandler(&tenantService{s}, opts...))
	return mux
}

func (s *Server) limits() {
	if s.ipLimit == nil {
		s.ipLimit = limit.New(60, time.Minute, 100_000)
		s.emailLimit = limit.New(10, time.Hour, 100_000)
	}
}

// tx runs fn scoped to the caller's tenant.
func (s *Server) tx(ctx context.Context, fn func(*db.Tx) error) error {
	p := auth.From(ctx)
	if p == nil || p.Tenant == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no principal"))
	}
	return s.DB.InTenant(ctx, p.Tenant, fn)
}

// audit records a staff write in the same transaction as the write.
func audit(ctx context.Context, tx *db.Tx, action, target string, detail map[string]any) error {
	p := auth.From(ctx)
	actor := "system"
	if p != nil && p.StaffID != "" {
		actor = "staff:" + p.StaffID
	}
	if detail == nil {
		detail = map[string]any{}
	}
	body, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	n := auditlog.Create()
	n.SetTenantID(tx.Tenant())
	n.SetActor(actor)
	n.SetAction(action)
	n.SetTarget(target)
	n.SetDetail(body)
	if _, err := n.Insert(ctx, tx); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// Error helpers. Internal errors are logged in full and returned bare, so a
// stack of SQL never reaches a client.

func notFound(what string) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s not found", what))
}

func invalidArg(err error) error { return connect.NewError(connect.CodeInvalidArgument, err) }

func failedPrecondition(msg string) error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
}

func (s *Server) internal(ctx context.Context, op string, err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, err)
	}
	s.Log.ErrorContext(ctx, "request failed", "op", op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

func isUniqueViolation(err error) bool { return errors.Is(err, runtime.ErrUniqueViolation) }

func isForeignKeyViolation(err error) bool { return errors.Is(err, runtime.ErrForeignKeyViolation) }

// uuid checks an id is shaped like one before it reaches a query, so a bad id
// is NotFound rather than a cast error.
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func msPtr(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}
