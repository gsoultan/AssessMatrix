package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/storm/runtime"

	"github.com/gsoultan/assessmatrix/services/core/internal/store/assignment"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/credential"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/itemscore"
	mediarow "github.com/gsoultan/assessmatrix/services/core/internal/store/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/outbox"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/person"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/webhookevent"

	"connectrpc.com/connect"
	"github.com/riverqueue/river"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/metrics"
)

// Retention and erasure (privacy track, docs/10 §5, docs/13).
//
// Retention: an attempt's record (its events: answers and integrity
// signals), the per-item index built from it and the images the candidate
// uploaded are deleted once the tenant's period (default 365 days) has passed
// since the exam. The attempt row keeps its score and says when and why its
// record went, so results, analysis and credentials keep working.
//
// Erasure: on request, everything about one person goes, the rows too.
//
// Both write an audit record with ids only.

// OutboxKeep is how long sent (or abandoned) mail stays for delivery questions.
const OutboxKeep = 30 * 24 * time.Hour

const purgeBatch = 200

// purgeAttempt deletes one attempt's record inside the tenant's transaction.
// The caller holds the attempt row lock. Returns the images deleted.
func (s *Server) purgeAttempt(ctx context.Context, tx *db.Tx, a *attemptRow, why string) (images int, err error) {
	images, err = s.deleteUploads(ctx, tx, "assignment:"+a.AssignmentID)
	if err != nil {
		return 0, err
	}
	scores, err := itemscore.New().Where(itemscore.AttemptID.Eq(key(a.ID))).Limit(maxItemScores).All(ctx, tx, nil)
	if err != nil {
		return 0, err
	}
	ops := make([]runtime.BatchOp, len(scores))
	for i, r := range scores {
		ops[i] = itemscore.DeleteOp(r.AttemptID, r.ItemID)
	}
	if err := runBatch(ctx, tx, ops); err != nil {
		return 0, err
	}
	// The event log is append-only to this role; the owner's function
	// is the one way to delete it.
	purged, _, err := queries.PurgeAttempt.One(ctx, tx, key(a.ID))
	if err != nil {
		return 0, err
	}
	events := purged.N
	// Milliseconds, the seal's resolution, so the stored time re-seals exactly.
	now := s.now().Truncate(time.Millisecond)
	a.PurgedAt = &now
	a.Seal = s.seal(a)
	m := attempt.MutateKey(key(a.ID))
	m.SetPurgedAt(now)
	m.SetPurgedWhy(why)
	m.SetSeal(a.Seal)
	if err := m.Update(ctx, tx); err != nil {
		return 0, err
	}
	metrics.RecordsPurged.WithLabelValues(why).Inc()
	return images, audit(ctx, tx, "attempt.purged", a.ID, map[string]any{"why": why, "events": events, "images": images})
}

// deleteUploads removes the images one uploader put there: the bytes first,
// so a failure leaves rows to retry from, never bytes no row points at.
func (s *Server) deleteUploads(ctx context.Context, tx *db.Tx, by string) (int, error) {
	objs, err := mediarow.New().Where(mediarow.UploadedBy.Eq(by)).Limit(maxUploadsPerSitting).All(ctx, tx, nil)
	if err != nil {
		return 0, err
	}
	ops := make([]runtime.BatchOp, len(objs))
	for i, o := range objs {
		id := idText(o.ID)
		k, err := media.Key(idText(o.TenantID), id)
		if err != nil {
			return 0, err
		}
		if s.Media.Store != nil {
			if err := s.Media.Delete(ctx, k); err != nil {
				return 0, fmt.Errorf("delete image %s: %w", id, err)
			}
		}
		ops[i] = mediarow.DeleteOp(o.ID)
	}
	return len(objs), runBatch(ctx, tx, ops)
}

// maxUploadsPerSitting bounds the images one sitting can leave behind, well
// above what a candidate uploads.
const maxUploadsPerSitting = 10_000

// eraseBatch is how many rows erasure reads and deletes at a time.
const eraseBatch = 1000

// runBatch sends a set of keyed writes in one round trip.
func runBatch(ctx context.Context, tx *db.Tx, ops []runtime.BatchOp) error {
	if len(ops) == 0 {
		return nil
	}
	return tx.Batch(ctx, ops, nil)
}

// PurgeDue applies retention to every tenant: records past their period are
// deleted, and so is mail older than OutboxKeep. Safe to run concurrently and
// repeatedly: each record is locked and checked again in its own transaction.
func (s *Server) PurgeDue(ctx context.Context) (purged int, err error) {
	now := s.now()
	for {
		batch, err := queries.RetentionDue.Query(ctx, s.DB.Executor(), now, purgeBatch)
		if err != nil {
			return purged, err
		}
		for _, d := range batch {
			attemptID := idText(d.AttemptID)
			err := s.DB.InTenant(ctx, db.TenantID(idText(d.TenantID)), func(tx *db.Tx) error {
				a, err := attemptBy(ctx, tx, "id", attemptID, true)
				if err != nil || a.PurgedAt != nil {
					return err // gone or done by another run meanwhile
				}
				_, err = s.purgeAttempt(ctx, tx, a, "retention")
				return err
			})
			if err != nil {
				return purged, fmt.Errorf("purge %s: %w", attemptID, err)
			}
			purged++
		}
		if len(batch) < purgeBatch {
			break
		}
	}
	if _, _, err := queries.OutboxSweep.One(ctx, s.DB.Executor(), now.Add(-OutboxKeep)); err != nil {
		return purged, err
	}
	if _, _, err := queries.WebhookSweep.One(ctx, s.DB.Executor(), now.Add(-WebhookKeep)); err != nil {
		return purged, err
	}
	return purged, nil
}

// RetentionArgs is the periodic retention job (cmd/core schedules it hourly).
type RetentionArgs struct{}

func (RetentionArgs) Kind() string { return "retention" }

// RetentionWorker runs PurgeDue. Server is set once the server exists.
type RetentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	Server *Server
}

func (w *RetentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	n, err := w.Server.PurgeDue(ctx)
	if n > 0 {
		w.Server.Log.Info("retention", "purged", n)
	}
	return err
}

func (s *personService) ErasePerson(ctx context.Context, req *connect.Request[v1.ErasePersonRequest]) (*connect.Response[v1.ErasePersonResponse], error) {
	id, why := req.Msg.GetId(), strings.TrimSpace(req.Msg.GetReason())
	if !validUUID(id) {
		return nil, notFound("person")
	}
	if why == "" || len(why) > 200 {
		return nil, invalidArg(errors.New("say why, in up to 200 characters, without personal data"))
	}
	out := &v1.ErasePersonResponse{}
	err := s.tx(ctx, func(tx *db.Tx) error {
		who, ok, err := person.New().IDEq(key(id)).ForUpdate().One(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return notFound("person")
		}
		rows, err := attempt.New().Where(attempt.PersonID.Eq(who.ID)).ForUpdate().Limit(maxExamRows).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a := attemptOf(r)
			if a.PurgedAt == nil {
				n, err := s.purgeAttempt(ctx, tx, a, "erasure")
				if err != nil {
					return err
				}
				out.Images += int32(n)
			}
			creds, err := credential.New().Where(credential.AttemptID.Eq(r.ID)).Limit(eraseBatch).All(ctx, tx, nil)
			if err != nil {
				return err
			}
			ops := make([]runtime.BatchOp, 0, len(creds)+1)
			for _, c := range creds {
				ops = append(ops, credential.DeleteOp(c.ID))
			}
			out.Credentials += int32(len(creds))
			if err := runBatch(ctx, tx, append(ops, attempt.DeleteOp(r.ID))); err != nil {
				return err
			}
			out.Attempts++
		}
		// Images from an invitation that never became an attempt.
		assignments, err := assignment.New().Where(assignment.PersonID.Eq(who.ID)).Limit(maxExamRows).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, a := range assignments {
			n, err := s.deleteUploads(ctx, tx, "assignment:"+idText(a.ID))
			if err != nil {
				return err
			}
			out.Images += int32(n)
		}
		// Mail to them, and the person with their invitations.
		if err := drain(ctx, tx, func() ([]runtime.BatchOp, error) {
			mail, err := outbox.New().Where(outbox.ToAddr.EqLower(who.Email)).Unordered().Limit(eraseBatch).All(ctx, tx, nil)
			ops := make([]runtime.BatchOp, len(mail))
			for i, m := range mail {
				ops[i] = outbox.DeleteOp(m.ID)
			}
			return ops, err
		}); err != nil {
			return err
		}
		// Webhook events about them, with their deliveries (ids only, but
		// the id of an erased person should not outlive them here).
		if err := drain(ctx, tx, func() ([]runtime.BatchOp, error) {
			evs, err := webhookevent.New().Where(webhookevent.PersonID.Eq(who.ID)).Unordered().Limit(eraseBatch).All(ctx, tx, nil)
			ops := make([]runtime.BatchOp, len(evs))
			for i, e := range evs {
				ops[i] = webhookevent.DeleteOp(e.ID)
			}
			return ops, err
		}); err != nil {
			return err
		}
		if err := person.Delete(ctx, tx, who.ID); err != nil {
			return err
		}
		return audit(ctx, tx, "person.erased", id, map[string]any{
			"why": why, "attempts": out.Attempts, "credentials": out.Credentials, "images": out.Images})
	})
	if err != nil {
		return nil, s.internal(ctx, "ErasePerson", err)
	}
	return connect.NewResponse(out), nil
}

// drain applies batches of keyed writes until next finds nothing left to
// write. Each batch must take its rows out of what next reads.
func drain(ctx context.Context, tx *db.Tx, next func() ([]runtime.BatchOp, error)) error {
	for {
		ops, err := next()
		if err != nil {
			return err
		}
		if err := runBatch(ctx, tx, ops); err != nil {
			return err
		}
		if len(ops) < eraseBatch {
			return nil
		}
	}
}
