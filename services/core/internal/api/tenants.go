package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/gsoultan/storm/runtime"

	"github.com/gsoultan/assessmatrix/services/core/internal/store/exam"
	mediarow "github.com/gsoultan/assessmatrix/services/core/internal/store/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staff"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staffcode"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staffsession"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/tenant"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// tenantService is tenancy as staff see it: which tenants they work in, who
// else does, and instruments shared between them.
//
// A super administrator creates tenants and decides who administers each. An
// administrator gives users access to the tenants they administer, as
// members, and shares instruments between them.
//
// Nothing here reads two tenants in one transaction. A request that touches
// several runs one transaction per tenant, each scoped by row-level security
// as usual, and each only after am_staff_tenants has shown the caller holds
// the right role there.
type tenantService struct{ *Server }

// Bounds on one request. Sharing copies images, so it is the expensive one.
const (
	maxShareTargets  = 50
	maxStaffInTenant = 2000
)

// errAdminsBySuper refuses an administrator a change to who administers.
var errAdminsBySuper = connect.NewError(connect.CodePermissionDenied,
	errors.New("only a super administrator can make someone an administrator or change an administrator's access"))

var (
	regionShape = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)?$`)
	emailShape  = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

// membership is the caller's place in one tenant.
type membership struct {
	tenant  db.TenantID
	staffID string
	name    string
	region  string
	role    string
}

// in returns ctx as the caller would be if signed in to m's tenant, so the
// audit log there names their staff row there. A super administrator who
// does not work in it is named by their own staff row.
func (m *membership) in(ctx context.Context) context.Context {
	p := *auth.From(ctx)
	p.Tenant, p.Role = m.tenant, m.role
	if m.staffID != "" {
		p.StaffID = m.staffID
	}
	return auth.With(ctx, &p)
}

// memberships is every tenant the signed-in staff member belongs to, found by
// their own address, never one from the request.
func (s *Server) memberships(ctx context.Context) (string, []membership, error) {
	p := auth.From(ctx)
	var email string
	if err := s.tx(ctx, func(tx *db.Tx) error {
		me, ok, err := staff.New().IDEq(key(p.StaffID)).One(ctx, tx)
		if err == nil && !ok {
			err = notFound("staff member")
		}
		email = me.Email
		return err
	}); err != nil {
		return "", nil, err
	}
	var out []membership
	err := s.DB.Unscoped(ctx, func(tx *db.Tx) error {
		rows, err := queries.StaffTenants.Query(ctx, tx, email)
		for _, r := range rows {
			out = append(out, membership{tenant: db.TenantID(idText(r.TenantID)), staffID: idText(r.StaffID),
				name: r.TenantName, region: r.Region, role: r.Role})
		}
		return err
	})
	return email, out, err
}

// find returns the caller's membership of a tenant. A tenant they do not
// belong to is not found, whether or not it exists.
func find(ms []membership, id string) (*membership, error) {
	for i := range ms {
		if string(ms[i].tenant) == id {
			return &ms[i], nil
		}
	}
	return nil, notFound("tenant")
}

// adminIn is find, refusing a membership that is not an admin's.
func adminIn(ms []membership, id string) (*membership, error) {
	m, err := find(ms, id)
	if err != nil {
		return nil, err
	}
	if m.role != auth.RoleAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("you are not an administrator of %s", m.name))
	}
	return m, nil
}

// managed is the tenants whose access the caller manages: the ones they
// administer, or every tenant for a super administrator. A tenant a super
// administrator does not work in has no staff id and no role.
func (s *Server) managed(ctx context.Context, ms []membership) ([]membership, error) {
	if !auth.From(ctx).Super {
		var out []membership
		for _, m := range ms {
			if m.role == auth.RoleAdmin {
				out = append(out, m)
			}
		}
		return out, nil
	}
	var out []membership
	err := s.DB.Unscoped(ctx, func(tx *db.Tx) error {
		rows, err := queries.AllTenants.Query(ctx, tx)
		for _, r := range rows {
			m := membership{tenant: db.TenantID(idText(r.TenantID)), name: r.TenantName, region: r.Region}
			if mine, err := find(ms, string(m.tenant)); err == nil {
				m = *mine
			}
			out = append(out, m)
		}
		return err
	})
	return out, err
}

func roleIn(r v1.Role) string {
	switch r {
	case v1.Role_ROLE_ADMIN:
		return auth.RoleAdmin
	case v1.Role_ROLE_MEMBER:
		return auth.RoleMember
	}
	return ""
}

func (m *membership) proto(current db.TenantID) *v1.Tenant {
	return &v1.Tenant{Id: string(m.tenant), Name: m.name, Region: m.region, Role: roleProto(m.role), Current: m.tenant == current}
}

func (s *tenantService) ListMyTenants(ctx context.Context, _ *connect.Request[v1.ListMyTenantsRequest]) (*connect.Response[v1.ListMyTenantsResponse], error) {
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ListMyTenants", err)
	}
	out := &v1.ListMyTenantsResponse{}
	for i := range ms {
		out.Tenants = append(out.Tenants, ms[i].proto(auth.From(ctx).Tenant))
	}
	return connect.NewResponse(out), nil
}

// SwitchTenant mints a session in the other tenant, then ends this one. A
// session never spans tenants, so every query after the switch is scoped to
// the new tenant by the same machinery as before it.
func (s *tenantService) SwitchTenant(ctx context.Context, req *connect.Request[v1.SwitchTenantRequest]) (*connect.Response[v1.SwitchTenantResponse], error) {
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "SwitchTenant", err)
	}
	m, err := find(ms, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	p := auth.From(ctx)
	out := &v1.SwitchTenantResponse{}
	if m.tenant == p.Tenant {
		err := s.tx(ctx, func(tx *db.Tx) (err error) {
			out.Staff, err = staffProto(ctx, tx, p.StaffID)
			return err
		})
		if err != nil {
			return nil, s.internal(ctx, "SwitchTenant", err)
		}
		return connect.NewResponse(out), nil
	}
	session := secure.Token()
	err = s.DB.InTenant(ctx, m.tenant, func(tx *db.Tx) error {
		n := staffsession.Create()
		n.SetTenantID(tx.Tenant())
		n.SetStaffID(key(m.staffID))
		n.SetTokenHash(s.Keys.SessionHash(session))
		n.SetExpiresAt(s.now().Add(auth.StaffSessionTTL))
		if _, err := n.Insert(ctx, tx); err != nil {
			return err
		}
		out.Staff, err = staffProto(ctx, tx, m.staffID)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "SwitchTenant", err)
	}
	if token := auth.CookieValue(req.Header(), auth.StaffCookie); secure.ValidToken(token) {
		if err := s.tx(ctx, func(tx *db.Tx) error {
			return revokeSessions(ctx, tx, staffsession.TokenHash.Eq(s.Keys.SessionHash(token)))
		}); err != nil {
			return nil, s.internal(ctx, "SwitchTenant", err)
		}
	}
	res := connect.NewResponse(out)
	s.Auth.SetCookie(res.Header(), auth.StaffCookie, session, auth.StaffSessionTTL)
	return res, nil
}

// CreateTenant makes a tenant and its first administrator: the person named,
// or the caller. Only a super administrator gets here.
func (s *tenantService) CreateTenant(ctx context.Context, req *connect.Request[v1.CreateTenantRequest]) (*connect.Response[v1.CreateTenantResponse], error) {
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" || len(name) > 200 {
		return nil, invalidArg(errors.New("a tenant needs a name of up to 200 characters"))
	}
	region := strings.TrimSpace(req.Msg.GetRegion())
	if region != "" && !regionShape.MatchString(region) {
		return nil, invalidArg(errors.New("region must be a two-letter code such as id or sg"))
	}
	adminEmail := strings.ToLower(strings.TrimSpace(req.Msg.GetAdminEmail()))
	if adminEmail != "" && (len(adminEmail) > 320 || !emailShape.MatchString(adminEmail)) {
		return nil, invalidArg(errors.New("the administrator needs a valid email address"))
	}
	adminName := strings.TrimSpace(req.Msg.GetAdminName())
	if len(adminName) > 200 {
		return nil, invalidArg(errors.New("a name is at most 200 characters"))
	}
	p := auth.From(ctx)
	var email, staffName, here string
	err := s.tx(ctx, func(tx *db.Tx) error {
		rows, err := staff.New().IDEq(key(p.StaffID)).Limit(1).AllWithTenant(ctx, tx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return notFound("staff member")
		}
		email, staffName, here = rows[0].Email, rows[0].Name, rows[0].Region
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "CreateTenant", err)
	}
	if region == "" {
		region = here
	}
	mine := adminEmail == "" || adminEmail == strings.ToLower(email)
	if mine {
		adminEmail, adminName = email, staffName
	}
	m := membership{tenant: db.TenantID(uuid.NewString()), name: name, region: region}
	// Row-level security lets a transaction insert only its own tenant's
	// rows, the new tenant included: it is scoped to the new id first.
	err = s.DB.InTenant(ctx, m.tenant, func(tx *db.Tx) error {
		t := tenant.Create()
		t.SetID(tx.Tenant())
		t.SetName(name)
		t.SetRegion(region)
		if _, err := t.Insert(ctx, tx); err != nil {
			return err
		}
		a := staff.Create()
		a.SetTenantID(tx.Tenant())
		a.SetEmail(adminEmail)
		a.SetName(adminName)
		a.SetRole(auth.RoleAdmin)
		admin, err := a.Insert(ctx, tx)
		if err != nil {
			return err
		}
		if mine {
			m.staffID, m.role = idText(admin.ID), auth.RoleAdmin
		}
		if err := audit(m.in(ctx), tx, "tenant.created", string(m.tenant), map[string]any{
			"name": name, "region": region, "from": string(p.Tenant), "admin": adminEmail,
		}); err != nil {
			return err
		}
		if mine {
			return nil
		}
		return s.accessMail(ctx, tx, adminEmail, name)
	})
	if err == nil {
		err = s.tx(ctx, func(tx *db.Tx) error {
			return audit(ctx, tx, "tenant.created", string(m.tenant), map[string]any{"name": name, "region": region, "admin": adminEmail})
		})
	}
	if err != nil {
		return nil, s.internal(ctx, "CreateTenant", err)
	}
	return connect.NewResponse(&v1.CreateTenantResponse{Tenant: m.proto(p.Tenant)}), nil
}

// accessMail tells someone they can now work in a tenant.
func (s *Server) accessMail(ctx context.Context, tx *db.Tx, email, tenant string) error {
	return s.queueMail(ctx, tx, "staff-access", email, "Anda mendapat akses ke "+tenant+" di AssessMatrix",
		"Anda sekarang dapat bekerja di "+tenant+" di AssessMatrix.\n\nMasuk dengan alamat email ini di "+
			strings.TrimRight(s.PublicURL, "/")+"/ ; kode masuk akan dikirim ke email ini.")
}

// staffRow is one active staff member of a tenant.
type staffRow struct {
	email, name, role string
	super             bool
}

// staffIn lists the active staff of one tenant, by address.
func staffIn(ctx context.Context, tx *db.Tx) ([]staffRow, error) {
	rows, err := staff.New().Where(staff.DisabledAt.IsNull()).Unordered().Limit(maxStaffInTenant).All(ctx, tx, nil)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	out := make([]staffRow, len(rows))
	emails := make([]string, len(rows))
	for i, r := range rows {
		out[i] = staffRow{email: strings.ToLower(r.Email), name: r.Name, role: r.Role}
		emails[i] = r.Email
	}
	supers, err := queries.SuperAdmins.Query(ctx, tx, emails)
	if err != nil {
		return nil, err
	}
	for _, sa := range supers {
		for i := range out {
			if out[i].email == strings.ToLower(sa.Email) {
				out[i].super = true
			}
		}
	}
	slices.SortFunc(out, func(a, b staffRow) int { return strings.Compare(a.email, b.email) })
	return out, nil
}

// revokeSessions ends every live staff session matching where.
func revokeSessions(ctx context.Context, tx *db.Tx, where staffsession.Pred) error {
	return drain(ctx, tx, func() ([]runtime.BatchOp, error) {
		live, err := staffsession.New().Where(where, staffsession.RevokedAt.IsNull()).Unordered().Limit(eraseBatch).All(ctx, tx, nil)
		ops := make([]runtime.BatchOp, 0, len(live))
		for _, r := range live {
			m := staffsession.Mutate(r)
			m.SetRevokedAtNow()
			if op, ok := m.UpdateOp(); ok {
				ops = append(ops, op)
			}
		}
		return ops, err
	})
}

// access is ListAccess's table over the tenants the caller manages,
// optionally for one address.
func (s *Server) access(ctx context.Context, managed []membership, only string) (*v1.ListAccessResponse, error) {
	out := &v1.ListAccessResponse{}
	byEmail := map[string]*v1.StaffAccess{}
	for i := range managed {
		m := &managed[i]
		out.Tenants = append(out.Tenants, m.proto(auth.From(ctx).Tenant))
		var rows []staffRow
		err := s.DB.InTenant(ctx, m.tenant, func(tx *db.Tx) (err error) {
			rows, err = staffIn(ctx, tx)
			return err
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if only != "" && r.email != only {
				continue
			}
			sa := byEmail[r.email]
			if sa == nil {
				sa = &v1.StaffAccess{Email: r.email, SuperAdmin: r.super}
				byEmail[r.email] = sa
				out.Staff = append(out.Staff, sa)
			}
			if sa.Name == "" {
				sa.Name = r.name
			}
			sa.Grants = append(sa.Grants, &v1.AccessGrant{TenantId: string(m.tenant), Role: roleProto(r.role)})
		}
	}
	slices.SortFunc(out.Staff, func(a, b *v1.StaffAccess) int { return strings.Compare(a.GetEmail(), b.GetEmail()) })
	return out, nil
}

func (s *tenantService) ListAccess(ctx context.Context, _ *connect.Request[v1.ListAccessRequest]) (*connect.Response[v1.ListAccessResponse], error) {
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ListAccess", err)
	}
	managed, err := s.managed(ctx, ms)
	if err != nil {
		return nil, s.internal(ctx, "ListAccess", err)
	}
	out, err := s.access(ctx, managed, "")
	if err != nil {
		return nil, s.internal(ctx, "ListAccess", err)
	}
	return connect.NewResponse(out), nil
}

// SetAccess grants, changes or withdraws one person's access to one tenant.
// Withdrawing ends their sessions there at once. A tenant always keeps at
// least one administrator, so nobody can lock everyone out of it.
//
// An administrator manages members of the tenants they administer. Making
// someone an administrator, or changing an administrator's access, is for a
// super administrator, who may do it in any tenant.
func (s *tenantService) SetAccess(ctx context.Context, req *connect.Request[v1.SetAccessRequest]) (*connect.Response[v1.SetAccessResponse], error) {
	email := strings.ToLower(strings.TrimSpace(req.Msg.GetEmail()))
	if len(email) > 320 || !emailShape.MatchString(email) {
		return nil, invalidArg(errors.New("enter a valid email address"))
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if len(name) > 200 {
		return nil, invalidArg(errors.New("a name is at most 200 characters"))
	}
	role := roleIn(req.Msg.GetRole())
	if req.Msg.GetRole() != v1.Role_ROLE_UNSPECIFIED && role == "" {
		return nil, invalidArg(errors.New("unknown role"))
	}
	super := auth.From(ctx).Super
	if role == auth.RoleAdmin && !super {
		return nil, errAdminsBySuper
	}
	by, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	managed, err := s.managed(ctx, ms)
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	var m *membership
	if super {
		m, err = find(managed, req.Msg.GetTenantId())
	} else {
		m, err = adminIn(ms, req.Msg.GetTenantId())
	}
	if err != nil {
		return nil, err
	}
	var added bool
	err = s.DB.InTenant(ctx, m.tenant, func(tx *db.Tx) error {
		// One change to a tenant's administrators at a time, so two admins
		// demoting each other cannot both pass the last-admin check.
		if err := lockTenant(ctx, tx); err != nil {
			return err
		}
		row, current, err := staff.New().Where(staff.Email.EqLower(email)).ForUpdate().One(ctx, tx)
		if err != nil {
			return err
		}
		id, was, active := idText(row.ID), row.Role, !row.DisabledAt.Valid
		if current && active && was == auth.RoleAdmin && !super {
			return errAdminsBySuper
		}
		if current && active && was == auth.RoleAdmin && role != auth.RoleAdmin {
			others, err := staff.New().
				Where(staff.Role.Eq(auth.RoleAdmin), staff.DisabledAt.IsNull(), staff.ID.NotEq(row.ID)).Count(ctx, tx)
			if err != nil {
				return err
			}
			if others == 0 {
				return failedPrecondition(m.name + " needs at least one administrator; make someone else an administrator first")
			}
		}
		if role == "" {
			if !current || !active {
				return nil
			}
			off := staff.Mutate(row)
			off.SetDisabledAtNow()
			if err := off.Update(ctx, tx); err != nil {
				return err
			}
			if err := revokeSessions(ctx, tx, staffsession.StaffID.Eq(row.ID)); err != nil {
				return err
			}
			if err := staffcode.Delete(ctx, tx, row.ID); err != nil && !noRow(err) {
				return err
			}
			return audit(m.in(ctx), tx, "staff.access", id, map[string]any{"email": email, "role": "none", "was": was, "by": by})
		}
		if current {
			// The row is locked, so re-granting cannot race a second grant.
			up := staff.Mutate(row)
			up.SetRole(role)
			up.SetDisabledAtNull()
			if name != "" {
				up.SetName(name)
			}
			if err := up.Update(ctx, tx); err != nil {
				return err
			}
		} else {
			n := staff.Create()
			n.SetTenantID(tx.Tenant())
			n.SetEmail(email)
			n.SetName(name)
			n.SetRole(role)
			made, err := n.Insert(ctx, tx)
			if err != nil {
				return err
			}
			id = idText(made.ID)
		}
		if !current || !active {
			was, added = "none", true
		}
		if err := audit(m.in(ctx), tx, "staff.access", id, map[string]any{"email": email, "role": role, "was": was, "by": by}); err != nil {
			return err
		}
		if !added {
			return nil
		}
		return s.accessMail(ctx, tx, email, m.name)
	})
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	// Their grants only: read just the managed tenants they are in, not
	// every tenant a super administrator could see.
	var theirs []string
	err = s.DB.Unscoped(ctx, func(tx *db.Tx) error {
		rows, err := queries.StaffTenants.Query(ctx, tx, email)
		for _, r := range rows {
			theirs = append(theirs, idText(r.TenantID))
		}
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	managed = slices.DeleteFunc(managed, func(x membership) bool { return !slices.Contains(theirs, string(x.tenant)) })
	out, err := s.access(ctx, managed, email)
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	res := &v1.SetAccessResponse{Staff: &v1.StaffAccess{Email: email}}
	if len(out.GetStaff()) > 0 {
		res.Staff = out.GetStaff()[0]
	}
	return connect.NewResponse(res), nil
}

/* ── sharing instruments ───────────────────────────────────────────────── */

// copyIn reads the copy of an original held by the tenant tx is scoped to.
func copyIn(ctx context.Context, tx *db.Tx, m *membership, sourceID string) (*v1.ExamCopy, error) {
	c := &v1.ExamCopy{TenantId: string(m.tenant), TenantName: m.name}
	r, ok, err := sharedCopy(ctx, tx, sourceID, false)
	if err != nil || !ok {
		return c, err
	}
	c.ExamId, c.SourceVersion, c.PublishedVersion, c.Status = idText(r.ID), r.SourceVersion.V, r.PublishedVersion, v1.Status(r.Status)
	return c, nil
}

// sharedCopy reads the tenant's copy of an original, if it holds one.
func sharedCopy(ctx context.Context, tx *db.Tx, sourceID string, lock bool) (exam.Row, bool, error) {
	q := exam.New().Where(exam.SourceExamID.Eq(key(sourceID)))
	if lock {
		q = q.ForUpdate()
	}
	return q.One(ctx, tx)
}

func (s *tenantService) ListExamCopies(ctx context.Context, req *connect.Request[v1.ListExamCopiesRequest]) (*connect.Response[v1.ListExamCopiesResponse], error) {
	id := req.Msg.GetExamId()
	err := s.tx(ctx, func(tx *db.Tx) error {
		_, err := getExam(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "ListExamCopies", err)
	}
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ListExamCopies", err)
	}
	out := &v1.ListExamCopiesResponse{}
	for i := range ms {
		m := &ms[i]
		if m.role != auth.RoleAdmin || m.tenant == auth.From(ctx).Tenant {
			continue
		}
		err := s.DB.InTenant(ctx, m.tenant, func(tx *db.Tx) error {
			c, err := copyIn(ctx, tx, m, id)
			out.Copies = append(out.Copies, c)
			return err
		})
		if err != nil {
			return nil, s.internal(ctx, "ListExamCopies", err)
		}
	}
	return connect.NewResponse(out), nil
}

// sharedImage is an uploaded image a shared version shows.
type sharedImage struct {
	url, contentType string
	bytes            int32
	sha              []byte
}

// original is the version being shared, read once in its own tenant.
type original struct {
	tenant     db.TenantID
	tenantName string
	exam       *v1.Exam
	version    int32
	images     []sharedImage
}

// uploadedImages walks every image an instrument shows.
func uploadedImages(e *v1.Exam, fn func(m *v1.Media)) {
	for _, sec := range e.GetSections() {
		for _, it := range sec.GetItems() {
			if m := it.GetImage(); m != nil {
				fn(m)
			}
			for _, c := range it.GetChoices() {
				if m := c.GetImage(); m != nil {
					fn(m)
				}
			}
		}
	}
}

func (s *tenantService) ShareExam(ctx context.Context, req *connect.Request[v1.ShareExamRequest]) (*connect.Response[v1.ShareExamResponse], error) {
	targets := slices.Compact(slices.Sorted(slices.Values(req.Msg.GetTenantIds())))
	if len(targets) == 0 {
		return nil, invalidArg(errors.New("choose at least one tenant to share with"))
	}
	if len(targets) > maxShareTargets {
		return nil, invalidArg(fmt.Errorf("share with at most %d tenants at a time", maxShareTargets))
	}
	src := &original{tenant: auth.From(ctx).Tenant}
	err := s.tx(ctx, func(tx *db.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetExamId(), false)
		if err != nil {
			return err
		}
		src.version = req.Msg.GetVersion()
		if src.version == 0 {
			src.version = e.GetPublishedVersion()
		}
		if src.version == 0 {
			return failedPrecondition("publish this instrument before sharing it")
		}
		if src.exam, err = examAt(ctx, tx, e.GetId(), src.version); err != nil {
			return err
		}
		src.exam.Id = e.GetId()
		here, ok, err := tenant.New().IDEq(tx.Tenant()).One(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return notFound("tenant")
		}
		src.tenantName = here.Name
		seen := map[string]bool{}
		var missing error
		uploadedImages(src.exam, func(m *v1.Media) {
			id, ok := strings.CutPrefix(m.GetUrl(), MediaPrefix)
			if !ok || seen[id] || missing != nil || !validUUID(id) {
				return
			}
			seen[id] = true
			row, ok, err := mediarow.New().IDEq(key(id)).One(ctx, tx)
			if err != nil {
				missing = err
				return
			}
			if !ok {
				return // already broken here; the copy shows it broken too
			}
			src.images = append(src.images, sharedImage{url: m.GetUrl(), contentType: row.ContentType, bytes: row.Bytes, sha: row.Sha256})
		})
		return missing
	})
	if err != nil {
		return nil, s.internal(ctx, "ShareExam", err)
	}
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ShareExam", err)
	}
	out := &v1.ShareExamResponse{}
	var shared []string
	for _, id := range targets {
		r := &v1.ShareResult{TenantId: id}
		out.Results = append(out.Results, r)
		m, err := adminIn(ms, id)
		if err == nil && m.tenant == src.tenant {
			err = failedPrecondition("That is the tenant this instrument is in.")
		}
		if err == nil {
			r.Copy, err = s.shareInto(ctx, src, m)
		}
		if err != nil {
			// Each target fails on its own, in words an administrator can act on.
			var ce *connect.Error
			if errors.As(err, &ce) {
				r.Error = ce.Message()
			} else {
				s.Log.ErrorContext(ctx, "share failed", "tenant", id, "err", err)
				r.Error = "It could not be shared there. Try again; if it keeps failing, tell your support contact."
			}
			continue
		}
		shared = append(shared, id)
	}
	if len(shared) > 0 {
		err := s.tx(ctx, func(tx *db.Tx) error {
			return audit(ctx, tx, "exam.shared", src.exam.GetId(), map[string]any{"version": src.version, "tenants": shared})
		})
		if err != nil {
			return nil, s.internal(ctx, "ShareExam", err)
		}
	}
	return connect.NewResponse(out), nil
}

// shareInto brings one tenant's copy up to the original's version and
// publishes it there. Images are copied into the tenant, so its candidates
// never read another tenant's objects; one it already holds is reused.
//
// The copy's earlier versions stay as they were: invitations and attempts
// pinned to them keep the paper they were given.
func (s *tenantService) shareInto(ctx context.Context, src *original, m *membership) (*v1.ExamCopy, error) {
	if len(src.images) > 0 && (s.Media.Store == nil || m.region != s.Media.Region) {
		return nil, failedPrecondition(fmt.Sprintf("Its images cannot be stored in %s's region (%s).", m.name, m.region))
	}
	ctx = m.in(ctx)
	var out *v1.ExamCopy
	err := s.DB.InTenant(ctx, m.tenant, func(tx *db.Tx) error {
		held, ok, err := sharedCopy(ctx, tx, src.exam.GetId(), true)
		if err != nil {
			return err
		}
		var copyID string
		if ok {
			copyID = idText(held.ID)
		}
		at := held.SourceVersion.V
		next := proto.Clone(src.exam).(*v1.Exam)
		next.SharedFrom = nil
		urls := map[string]string{}
		for _, img := range src.images {
			if urls[img.url], err = s.copyImage(ctx, tx, src.tenant, img); err != nil {
				return err
			}
		}
		uploadedImages(next, func(im *v1.Media) {
			if u, ok := urls[im.GetUrl()]; ok {
				im.Url = u
			}
		})

		var cur *v1.Exam
		if copyID == "" {
			if cur, err = insertExam(ctx, tx, next); err != nil {
				return err
			}
		} else if cur, err = getExam(ctx, tx, copyID, true); err != nil {
			return err
		}
		if copyID != "" && at == src.version && cur.GetStatus() == v1.Status_STATUS_PUBLISHED && cur.GetPublishedVersion() == cur.GetVersion() {
			out, err = copyIn(ctx, tx, m, src.exam.GetId())
			return err // already holds this version, live
		}
		// A new version after a live one; over an unpublished draft, which
		// sharing replaces, the draft's own number.
		next.Id, next.Status, next.Version = cur.GetId(), v1.Status_STATUS_PUBLISHED, cur.GetVersion()
		if cur.GetPublishedVersion() == cur.GetVersion() {
			next.Version++
		}
		next.PublishedVersion = next.GetVersion()
		if _, err := writeExam(ctx, tx, next); err != nil {
			return err
		}
		from := exam.MutateKey(key(cur.GetId()))
		from.SetSourceExamID(key(src.exam.GetId()))
		from.SetSourceVersion(src.version)
		from.SetSourceTenantName(src.tenantName)
		if err := from.Update(ctx, tx); err != nil {
			return err
		}
		published, err := getExam(ctx, tx, cur.GetId(), false)
		if err != nil {
			return err
		}
		if err := snapshot(ctx, tx, published, fmt.Sprintf("shared from %s, version %d", src.tenantName, src.version)); err != nil {
			return err
		}
		if err := audit(ctx, tx, "exam.shared_in", cur.GetId(), map[string]any{
			"from_tenant": string(src.tenant), "from_exam": src.exam.GetId(), "from_version": src.version, "version": next.GetVersion(),
		}); err != nil {
			return err
		}
		out, err = copyIn(ctx, tx, m, src.exam.GetId())
		return err
	})
	return out, err
}

// copyImage puts one of the original's images in the tenant tx is scoped to
// and returns its URL there.
func (s *tenantService) copyImage(ctx context.Context, tx *db.Tx, from db.TenantID, img sharedImage) (string, error) {
	held, ok, err := mediarow.New().
		Where(mediarow.Sha256.Eq(img.sha), mediarow.ContentType.Eq(img.contentType)).Unordered().One(ctx, tx)
	if err != nil {
		return "", err
	}
	if ok {
		return MediaPrefix + idText(held.ID), nil
	}
	srcKey, err := media.Key(string(from), strings.TrimPrefix(img.url, MediaPrefix))
	if err != nil {
		return "", err
	}
	r, err := s.Media.Get(ctx, srcKey)
	if err != nil {
		return "", fmt.Errorf("read image %s: %w", srcKey, err)
	}
	body, err := io.ReadAll(io.LimitReader(r, media.MaxBytes+1))
	r.Close()
	if err != nil {
		return "", err
	}
	if len(body) > media.MaxBytes {
		return "", fmt.Errorf("image %s is larger than an upload may be", srcKey)
	}
	p := auth.From(ctx)
	n := mediarow.Create()
	n.SetTenantID(tx.Tenant())
	n.SetContentType(img.contentType)
	n.SetBytes(int32(len(body)))
	n.SetSha256(img.sha)
	n.SetUploadedBy("staff:" + p.StaffID)
	row, err := n.Insert(ctx, tx)
	if err != nil {
		return "", err
	}
	id := idText(row.ID)
	k, err := media.Key(string(p.Tenant), id)
	if err != nil {
		return "", err
	}
	// Stored before the row commits, as an upload is.
	if err := s.Media.Put(ctx, k, img.contentType, body); err != nil {
		return "", err
	}
	return MediaPrefix + id, nil
}
