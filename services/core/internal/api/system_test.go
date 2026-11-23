package api

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
	"github.com/gsoultan/assessmatrix/services/core/internal/update"
)

const testDigest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type stubFeed []update.Release

func (f stubFeed) Releases(context.Context, update.Version) ([]update.Release, error) { return f, nil }

func stubRelease(v, minFrom string, security bool) update.Release {
	ver, _ := update.ParseVersion(v)
	return update.Release{Version: ver, Notes: "Notes for " + v, Manifest: update.Manifest{Version: v,
		Image: "ghcr.io/acme/assessmatrix" + testDigest, MinUpgradeFrom: minFrom, Security: security}}
}

// withRelease makes the harness's server a v1.0.0 build whose feed offers
// v1.1.0, and v2.0.0 only from v1.1.0; and clears any update a previous run
// left waiting, since updates belong to the whole installation.
func (h *harness) withReleases() {
	h.t.Helper()
	h.srv.Version = "v1.0.0"
	h.srv.Updates = update.NewChecker(stubFeed{stubRelease("v1.1.0", "", true), stubRelease("v2.0.0", "v1.1.0", false)}, "v1.0.0", nil)
	if _, err := h.owner.Exec(context.Background(), `UPDATE system_updates SET state = 'cancelled' WHERE state IN ('requested', 'running')`); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) claim(host string) (queries.ClaimedUpdate, bool) {
	h.t.Helper()
	c, ok, err := queries.UpdateClaim.One(context.Background(), db.Conn(h.owner), host)
	if err != nil {
		h.t.Fatal(err)
	}
	return c, ok
}

// A super administrator sees the next release, asks for it, and the
// updater on the host claims it as the owner and reports back. The release
// installed is the one the feed pinned, whatever the request says.
func TestSuperAdminUpdatesTheInstallation(t *testing.T) {
	h := newHarness(t)
	h.withReleases()
	ctx := context.Background()
	super := h.superAdmin("Update HQ")

	st := get(super.system.GetUpdateStatus(ctx, connect.NewRequest(&v1.GetUpdateStatusRequest{Refresh: true}))).Msg
	if st.GetCurrentVersion() != "v1.0.0" || !st.GetChecksEnabled() || st.GetSchemaVersion() != db.LatestMigration() {
		t.Fatalf("status %v", st)
	}
	// v2.0.0 needs v1.1.0 first: that is the step offered.
	if st.GetNext().GetVersion() != "v1.1.0" || st.GetLatest().GetVersion() != "v2.0.0" ||
		!st.GetNext().GetSecurity() || st.GetNext().GetKind() != "minor" {
		t.Fatalf("next %v latest %v", st.GetNext(), st.GetLatest())
	}

	// Only the release on offer.
	for _, v := range []string{"v2.0.0", "v0.9.0", "latest"} {
		if _, err := super.system.RequestUpdate(ctx, connect.NewRequest(&v1.RequestUpdateRequest{Version: v})); err == nil {
			t.Errorf("RequestUpdate(%s) was accepted", v)
		}
	}

	// A sitting in progress: the update waits for it.
	f := h.publishedFixture(super)
	cand := h.redeem(f)
	get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents})))
	run := get(super.system.RequestUpdate(ctx, connect.NewRequest(&v1.RequestUpdateRequest{Version: "v1.1.0"}))).Msg.GetRun()
	if run.GetState() != v1.UpdateState_UPDATE_STATE_REQUESTED || !run.GetWaitForIdle() || run.GetRequestedBy() != super.me(t).GetEmail() {
		t.Fatalf("run %v", run)
	}
	if _, err := super.system.RequestUpdate(ctx, connect.NewRequest(&v1.RequestUpdateRequest{Version: "v1.1.0"})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a second request while one waits: %v, want failed precondition", err)
	}
	if _, ok := h.claim("host-a"); ok {
		t.Error("the updater was handed an update while a sitting is in progress")
	}
	st = get(super.system.GetUpdateStatus(ctx, connect.NewRequest(&v1.GetUpdateStatusRequest{}))).Msg
	if !st.GetUpdaterConnected() || st.GetSittingsInProgress() < 1 || st.GetRuns()[0].GetId() != run.GetId() {
		t.Errorf("status after the updater looked: connected %v sittings %d runs %v", st.GetUpdaterConnected(), st.GetSittingsInProgress(), st.GetRuns())
	}

	// Withdrawn, then asked for again without waiting.
	get(super.system.CancelUpdate(ctx, connect.NewRequest(&v1.CancelUpdateRequest{})))
	if _, err := super.system.CancelUpdate(ctx, connect.NewRequest(&v1.CancelUpdateRequest{})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("cancelling nothing: %v", err)
	}
	run = get(super.system.RequestUpdate(ctx, connect.NewRequest(&v1.RequestUpdateRequest{Version: "v1.1.0", Now: true}))).Msg.GetRun()
	claimed, ok := h.claim("host-a")
	if !ok || idText(claimed.ID) != run.GetId() || claimed.TargetVersion != "v1.1.0" || claimed.Image != "ghcr.io/acme/assessmatrix"+testDigest {
		t.Fatalf("claimed %+v (%v)", claimed, ok)
	}
	if _, again := h.claim("host-a"); again {
		t.Error("a running update was handed out twice")
	}
	// Started: no longer cancellable.
	if _, err := super.system.CancelUpdate(ctx, connect.NewRequest(&v1.CancelUpdateRequest{})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("cancelling a running update: %v", err)
	}
	report := func(state, msg string) bool {
		r, _, err := queries.UpdateReport.One(ctx, db.Conn(h.owner), claimed.ID, state, msg)
		if err != nil {
			t.Fatal(err)
		}
		return r.Yes
	}
	if !report("running", "Backed up") || !report("succeeded", "Now on v1.1.0") {
		t.Fatal("the updater's reports were refused")
	}
	if report("failed", "late") {
		t.Error("a finished update was reopened")
	}
	st = get(super.system.GetUpdateStatus(ctx, connect.NewRequest(&v1.GetUpdateStatusRequest{}))).Msg
	if r := st.GetRuns()[0]; r.GetState() != v1.UpdateState_UPDATE_STATE_SUCCEEDED || r.GetMessage() != "Now on v1.1.0" || r.GetFinishedAt() == 0 {
		t.Errorf("last run %v", r)
	}
}

// The application role, which every request runs as, can neither claim nor
// close an update, nor touch the tables: only the updater, as the owner, can.
func TestOnlyTheUpdaterClaimsUpdates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, sql := range []string{
		`SELECT * FROM am_update_claim('browser')`,
		`SELECT am_update_report(gen_random_uuid(), 'succeeded', 'x')`,
		`SELECT count(*) FROM system_updates`,
		`UPDATE system_updater SET host = 'x'`,
	} {
		err := h.srv.DB.Unscoped(ctx, func(tx *db.Tx) error {
			_, err := tx.Pgx().Exec(ctx, sql)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s as the application role: %v, want permission denied", sql, err)
		}
	}
}

// Members and tenant administrators do not see the installation's updates.
func TestUpdatesAreForSuperAdministrators(t *testing.T) {
	h := newHarness(t)
	h.withReleases()
	ctx := context.Background()
	admin := h.tenant("Plain Org")
	if _, err := admin.system.GetUpdateStatus(ctx, connect.NewRequest(&v1.GetUpdateStatusRequest{})); code(err) != connect.CodePermissionDenied {
		t.Errorf("a tenant administrator read the update status: %v", err)
	}
}
