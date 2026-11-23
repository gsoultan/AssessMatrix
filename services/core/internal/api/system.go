package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/gsoultan/storm/runtime"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/staff"
	"github.com/gsoultan/assessmatrix/services/core/internal/update"
)

// systemService is the installation itself: its version and its updates.
// Super administrators only (Policy). It records what they ask for; the
// updater on the host does it (deploy/updater).
type systemService struct{ *Server }

// UpdaterFresh is how recently the updater must have looked for work for
// the studio to offer the Update button. It looks once a minute.
const UpdaterFresh = 5 * time.Minute

func (s *Server) updates() *update.Checker {
	if s.Updates == nil {
		return update.NewChecker(nil, s.Version, nil)
	}
	return s.Updates
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func unixNull(t runtime.Null[time.Time]) int64 {
	if !t.Valid {
		return 0
	}
	return t.V.Unix()
}

func releaseProto(cur string, r *update.Release) *v1.Release {
	if r == nil {
		return nil
	}
	kind := ""
	if c, err := update.ParseVersion(cur); err == nil {
		kind = c.Kind(r.Version)
	}
	return &v1.Release{Version: r.Version.String(), PublishedAt: unix(r.PublishedAt), NotesUrl: r.NotesURL,
		Notes: r.Notes, Security: r.Manifest.Security, Kind: kind}
}

var updateStates = map[string]v1.UpdateState{
	"requested":   v1.UpdateState_UPDATE_STATE_REQUESTED,
	"running":     v1.UpdateState_UPDATE_STATE_RUNNING,
	"succeeded":   v1.UpdateState_UPDATE_STATE_SUCCEEDED,
	"failed":      v1.UpdateState_UPDATE_STATE_FAILED,
	"rolled_back": v1.UpdateState_UPDATE_STATE_ROLLED_BACK,
	"cancelled":   v1.UpdateState_UPDATE_STATE_CANCELLED,
}

func runProto(r queries.UpdateRunRow) *v1.UpdateRun {
	return &v1.UpdateRun{Id: idText(r.ID), FromVersion: r.FromVersion, TargetVersion: r.TargetVersion,
		State: updateStates[r.State], RequestedBy: r.RequestedBy, RequestedAt: unix(r.RequestedAt),
		StartedAt: unixNull(r.StartedAt), FinishedAt: unixNull(r.FinishedAt), Message: r.Message, WaitForIdle: r.WaitForIdle}
}

func (s *systemService) GetUpdateStatus(ctx context.Context, req *connect.Request[v1.GetUpdateStatusRequest]) (*connect.Response[v1.GetUpdateStatusResponse], error) {
	checker := s.updates()
	st := checker.Status()
	// Never checked yet (a server just started): check now rather than
	// show "up to date" on no evidence.
	if req.Msg.GetRefresh() || (st.Enabled && st.CheckedAt.IsZero()) {
		st = checker.Check(ctx)
	}
	out := &v1.GetUpdateStatusResponse{CurrentVersion: s.Version, ChecksEnabled: st.Enabled,
		CheckedAt: unix(st.CheckedAt), CheckError: st.Err,
		Next: releaseProto(st.Current, st.Plan.Next), Latest: releaseProto(st.Current, st.Plan.Latest)}
	if out.CurrentVersion == "" {
		out.CurrentVersion = "dev"
	}
	err := s.DB.Unscoped(ctx, func(tx *db.Tx) error {
		v, _, err := queries.SchemaVersion.One(ctx, tx)
		if err != nil {
			return err
		}
		out.SchemaVersion = v.Version
		seen, _, err := queries.UpdaterSeen.One(ctx, tx)
		if err != nil {
			return err
		}
		out.UpdaterSeenAt = unixNull(seen.SeenAt)
		out.UpdaterConnected = seen.SeenAt.Valid && s.now().Sub(seen.SeenAt.V) < UpdaterFresh
		n, _, err := queries.SittingsInProgress.One(ctx, tx)
		if err != nil {
			return err
		}
		out.SittingsInProgress = n.N
		runs, err := queries.Updates.Query(ctx, tx, 10)
		for _, r := range runs {
			out.Runs = append(out.Runs, runProto(r))
		}
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "GetUpdateStatus", err)
	}
	return connect.NewResponse(out), nil
}

func (s *systemService) myEmail(ctx context.Context) (string, error) {
	var email string
	err := s.tx(ctx, func(tx *db.Tx) error {
		me, ok, err := staff.New().IDEq(key(auth.From(ctx).StaffID)).One(ctx, tx)
		if err == nil && !ok {
			err = notFound("staff member")
		}
		email = me.Email
		return err
	})
	return email, err
}

func (s *systemService) RequestUpdate(ctx context.Context, req *connect.Request[v1.RequestUpdateRequest]) (*connect.Response[v1.RequestUpdateResponse], error) {
	st := s.updates().Status()
	if !st.Enabled {
		return nil, failedPrecondition("update checks are off for this server (a development build, or AM_UPDATE_CHECK=off)")
	}
	// Only the release the feed offers next, with the image its manifest
	// pins: nothing in the request names what gets installed.
	next := st.Plan.Next
	want, err := update.ParseVersion(req.Msg.GetVersion())
	if err != nil {
		return nil, invalidArg(err)
	}
	if next == nil || next.Version.Compare(want) != 0 {
		return nil, failedPrecondition("that is not the release this server can update to now; check again")
	}
	email, err := s.myEmail(ctx)
	if err != nil {
		return nil, s.internal(ctx, "RequestUpdate", err)
	}
	var run *v1.UpdateRun
	err = s.tx(ctx, func(tx *db.Tx) error {
		id, _, err := queries.RequestUpdate.One(ctx, tx, st.Current, next.Version.String(), next.Manifest.Image,
			next.Manifest.PsychometricsImage, email, !req.Msg.GetNow())
		if err != nil {
			if isUniqueViolation(err) {
				return failedPrecondition("an update is already waiting or running")
			}
			return err
		}
		run = &v1.UpdateRun{Id: idText(id.ID), FromVersion: st.Current, TargetVersion: next.Version.String(),
			State: v1.UpdateState_UPDATE_STATE_REQUESTED, RequestedBy: email, RequestedAt: s.now().Unix(), WaitForIdle: !req.Msg.GetNow()}
		return audit(ctx, tx, "system.update_requested", run.Id, map[string]any{
			"from": st.Current, "to": run.TargetVersion, "image": next.Manifest.Image, "wait_for_idle": run.WaitForIdle})
	})
	if err != nil {
		return nil, s.internal(ctx, "RequestUpdate", err)
	}
	s.Log.InfoContext(ctx, "update requested", "from", st.Current, "to", run.TargetVersion, "wait_for_idle", run.WaitForIdle)
	return connect.NewResponse(&v1.RequestUpdateResponse{Run: run}), nil
}

func (s *systemService) CancelUpdate(ctx context.Context, _ *connect.Request[v1.CancelUpdateRequest]) (*connect.Response[v1.CancelUpdateResponse], error) {
	email, err := s.myEmail(ctx)
	if err != nil {
		return nil, s.internal(ctx, "CancelUpdate", err)
	}
	var done bool
	err = s.tx(ctx, func(tx *db.Tx) error {
		r, _, err := queries.CancelUpdate.One(ctx, tx, email)
		if err != nil || !r.Yes {
			return err
		}
		done = true
		return audit(ctx, tx, "system.update_cancelled", "", nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "CancelUpdate", err)
	}
	if !done {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no update is waiting; one that has started runs to the end"))
	}
	return connect.NewResponse(&v1.CancelUpdateResponse{Cancelled: true}), nil
}
