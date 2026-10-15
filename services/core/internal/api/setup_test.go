package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1/assessmatrixv1connect"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/limit"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// setupDB names the database next to the shared test one with "_setup"
// appended. First-run setup needs a database with no organisation in it,
// which the shared one never is; CI creates this one for it.
func setupDB(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "_setup"
	return u.String()
}

// freshInstall is a server on an empty, migrated database.
func freshInstall(t *testing.T) (*Server, string, *pgx.Conn) {
	t.Helper()
	appURL, ownerURL := os.Getenv("AM_TEST_DATABASE_URL"), os.Getenv("AM_TEST_MIGRATE_DATABASE_URL")
	if appURL == "" || ownerURL == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("AM_TEST_DATABASE_URL and AM_TEST_MIGRATE_DATABASE_URL must be set in CI")
		}
		t.Skip("AM_TEST_DATABASE_URL and AM_TEST_MIGRATE_DATABASE_URL are not set")
	}
	appURL, ownerURL = setupDB(t, appURL), setupDB(t, ownerURL)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, ownerURL, "am_app"); err != nil {
		t.Fatalf("migrate the setup database (create it next to the test database, owned by the owner role): %v", err)
	}
	owner, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(context.Background()) })
	// Back to a fresh install, whatever an earlier run left.
	if _, err := owner.Exec(ctx, `TRUNCATE tenants CASCADE`); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(ctx, appURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := database.CheckRole(ctx); err != nil {
		t.Fatal(err)
	}
	keys, err := secure.NewKeys([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		DB: database, Keys: keys,
		Auth:      &auth.Authenticator{DB: database, Keys: keys, Policy: Policy()},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		PublicURL: "http://exam.test",
		Media:     media.Regional{Region: "id"},
	}
	srv.ipLimit, srv.emailLimit = limit.New(1_000_000, time.Minute, 1000), limit.New(1_000_000, time.Minute, 1000)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return srv, hs.URL, owner
}

type setupBrowser struct {
	setup assessmatrixv1connect.SetupServiceClient
	auth  assessmatrixv1connect.AuthServiceClient
}

func newSetupBrowser(base string) setupBrowser {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	return setupBrowser{assessmatrixv1connect.NewSetupServiceClient(hc, base), assessmatrixv1connect.NewAuthServiceClient(hc, base)}
}

// The /setup gate: an empty install asks for setup; a wrong code changes
// nothing; two right ones racing make exactly one organisation; the winner is
// signed in as its administrator; after that, setup is closed for good.
func TestFirstRunSetupHappensOnce(t *testing.T) {
	srv, base, owner := freshInstall(t)
	ctx := context.Background()
	b := newSetupBrowser(base)

	st := get(b.setup.GetSetupStatus(ctx, connect.NewRequest(&v1.GetSetupStatusRequest{}))).Msg
	if !st.GetNeeded() {
		t.Fatal("an empty database should ask for setup")
	}
	if st.GetSchemaVersion() != db.LatestMigration() || st.GetSchemaLatest() != db.LatestMigration() {
		t.Fatalf("schema %q, latest %q; want both %q", st.GetSchemaVersion(), st.GetSchemaLatest(), db.LatestMigration())
	}
	if st.GetRegion() != "id" {
		t.Fatalf("region %q, want the server's (id)", st.GetRegion())
	}

	req := func(code, email string) *connect.Request[v1.CompleteSetupRequest] {
		return connect.NewRequest(&v1.CompleteSetupRequest{SetupCode: code, OrganizationName: "Lembaga Uji",
			AdminEmail: email, AdminName: "Admin"})
	}
	if _, err := b.setup.CompleteSetup(ctx, req("AAAA-AAAA-AAAA", "admin@example.test")); code(err) != connect.CodePermissionDenied {
		t.Fatalf("a wrong setup code: got %v, want PermissionDenied", err)
	}
	if _, err := b.setup.CompleteSetup(ctx, req(srv.Keys.SetupCode(), "not-an-email")); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("a bad email: got %v, want InvalidArgument", err)
	}
	if !get(b.setup.GetSetupStatus(ctx, connect.NewRequest(&v1.GetSetupStatusRequest{}))).Msg.GetNeeded() {
		t.Fatal("a refused setup must leave the install open")
	}

	// Two operators finish setup at the same moment, typing the code as read.
	typed := strings.ToLower(srv.Keys.SetupCode())
	browsers := []setupBrowser{newSetupBrowser(base), newSetupBrowser(base)}
	errs := make([]error, 2)
	staff := make([]*v1.Staff, 2)
	var wg sync.WaitGroup
	for i := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := browsers[i].setup.CompleteSetup(ctx, req(typed, []string{"a@example.test", "b@example.test"}[i]))
			errs[i] = err
			if err == nil {
				staff[i] = res.Msg.GetStaff()
			}
		}()
	}
	wg.Wait()
	won := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if won >= 0 {
				t.Fatal("both racing setups succeeded")
			}
			won = i
		case code(err) != connect.CodeFailedPrecondition:
			t.Fatalf("the losing setup: got %v, want FailedPrecondition", err)
		}
	}
	if won < 0 {
		t.Fatalf("neither racing setup succeeded: %v", errs)
	}
	if staff[won].GetTenantName() != "Lembaga Uji" {
		t.Fatalf("the administrator is in %q", staff[won].GetTenantName())
	}

	// The winner is signed in by the cookie setup set.
	me := get(browsers[won].auth.WhoAmI(ctx, connect.NewRequest(&v1.WhoAmIRequest{}))).Msg.GetStaff()
	if me.GetId() != staff[won].GetId() {
		t.Fatalf("WhoAmI after setup: %v", me)
	}

	if after := get(b.setup.GetSetupStatus(ctx, connect.NewRequest(&v1.GetSetupStatusRequest{}))).Msg; after.GetNeeded() {
		t.Fatal("setup must close once an organisation exists")
	} else if after.GetSchemaVersion() != "" || after.GetRegion() != "" {
		t.Fatalf("a set-up server tells anyone who asks more than that it is set up: %v", after)
	}
	if _, err := b.setup.CompleteSetup(ctx, req(srv.Keys.SetupCode(), "late@example.test")); code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("setup after setup: got %v, want FailedPrecondition", err)
	}

	var tenants, admins, audits int
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM tenants), (SELECT count(*) FROM staff),
		(SELECT count(*) FROM audit_log WHERE action = 'setup.complete' AND actor = 'setup')`).Scan(&tenants, &admins, &audits); err != nil {
		t.Fatal(err)
	}
	if tenants != 1 || admins != 1 || audits != 1 {
		t.Fatalf("after setup: %d tenants, %d staff, %d audit rows; want 1 each", tenants, admins, audits)
	}

	// The serving role cannot make organisations by itself, setup or not.
	err := srv.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT am_create_tenant('Rogue', 'id', 'x@example.test', '')`)
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("the app role calling am_create_tenant: got %v, want permission denied", err)
	}
}
