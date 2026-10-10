package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
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
func (s *Server) purgeAttempt(ctx context.Context, tx pgx.Tx, a *attemptRow, why string) (images int, err error) {
	images, err = s.deleteUploads(ctx, tx, "assignment:"+a.AssignmentID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM item_scores WHERE attempt_id = $1`, a.ID); err != nil {
		return 0, err
	}
	var events int64
	if err := tx.QueryRow(ctx, `SELECT am_purge_attempt($1)`, a.ID).Scan(&events); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE attempts SET purged_at = $2, purged_why = $3 WHERE id = $1`, a.ID, s.now(), why); err != nil {
		return 0, err
	}
	metrics.RecordsPurged.WithLabelValues(why).Inc()
	return images, audit(ctx, tx, "attempt.purged", a.ID, map[string]any{"why": why, "events": events, "images": images})
}

// deleteUploads removes the images one uploader put there: the bytes first,
// so a failure leaves rows to retry from, never bytes no row points at.
func (s *Server) deleteUploads(ctx context.Context, tx pgx.Tx, by string) (int, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id::text, id::text FROM media WHERE uploaded_by = $1`, by)
	if err != nil {
		return 0, err
	}
	type obj struct{ Tenant, ID string }
	objs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[obj])
	if err != nil {
		return 0, err
	}
	for _, o := range objs {
		key, err := media.Key(o.Tenant, o.ID)
		if err != nil {
			return 0, err
		}
		if s.Media.Store != nil {
			if err := s.Media.Delete(ctx, key); err != nil {
				return 0, fmt.Errorf("delete image %s: %w", o.ID, err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media WHERE uploaded_by = $1`, by); err != nil {
		return 0, err
	}
	return len(objs), nil
}

// PurgeDue applies retention to every tenant: records past their period are
// deleted, and so is mail older than OutboxKeep. Safe to run concurrently and
// repeatedly: each record is locked and checked again in its own transaction.
func (s *Server) PurgeDue(ctx context.Context) (purged int, err error) {
	now := s.now()
	for {
		rows, err := s.DB.Pool.Query(ctx, `SELECT tenant_id::text, attempt_id::text FROM am_retention_due($1, $2)`, now, purgeBatch)
		if err != nil {
			return purged, err
		}
		type due struct{ Tenant, Attempt string }
		batch, err := pgx.CollectRows(rows, pgx.RowToStructByPos[due])
		if err != nil {
			return purged, err
		}
		for _, d := range batch {
			err := s.DB.InTenant(ctx, db.TenantID(d.Tenant), func(tx pgx.Tx) error {
				a, err := attemptBy(ctx, tx, "id", d.Attempt, true)
				if err != nil || a.PurgedAt != nil {
					return err // gone or done by another run meanwhile
				}
				_, err = s.purgeAttempt(ctx, tx, a, "retention")
				return err
			})
			if err != nil {
				return purged, fmt.Errorf("purge %s: %w", d.Attempt, err)
			}
			purged++
		}
		if len(batch) < purgeBatch {
			break
		}
	}
	if _, err := s.DB.Pool.Exec(ctx, `SELECT am_outbox_sweep($1)`, now.Add(-OutboxKeep)); err != nil {
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
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var email string
		if err := tx.QueryRow(ctx, `SELECT email FROM people WHERE id = $1 FOR UPDATE`, id).Scan(&email); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return notFound("person")
			}
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+attemptColumns+` FROM attempts WHERE person_id = $1 FOR UPDATE`, id)
		if err != nil {
			return err
		}
		atts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*attemptRow, error) { return scanAttempt(r) })
		if err != nil {
			return err
		}
		for _, a := range atts {
			if a.PurgedAt == nil {
				n, err := s.purgeAttempt(ctx, tx, a, "erasure")
				if err != nil {
					return err
				}
				out.Images += int32(n)
			}
			tag, err := tx.Exec(ctx, `DELETE FROM credentials WHERE attempt_id = $1`, a.ID)
			if err != nil {
				return err
			}
			out.Credentials += int32(tag.RowsAffected())
			if _, err := tx.Exec(ctx, `DELETE FROM attempts WHERE id = $1`, a.ID); err != nil {
				return err
			}
			out.Attempts++
		}
		// Images from an invitation that never became an attempt.
		arows, err := tx.Query(ctx, `SELECT id::text FROM assignments WHERE person_id = $1`, id)
		if err != nil {
			return err
		}
		assignments, err := pgx.CollectRows(arows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, a := range assignments {
			n, err := s.deleteUploads(ctx, tx, "assignment:"+a)
			if err != nil {
				return err
			}
			out.Images += int32(n)
		}
		// Mail to them, and the person with their invitations.
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE lower(to_addr) = lower($1)`, email); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM people WHERE id = $1`, id); err != nil {
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
