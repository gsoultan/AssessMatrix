package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// Bounds on what one attempt can store.
const (
	MaxBatch          = 500
	MaxEventsPerTry   = 100_000
	MaxKindLen        = 64
	MaxDetailLen      = 500
	MaxValuesPerEvent = domain.MaxChoices
)

type attemptRow struct {
	ID, AssignmentID, ExamID, PersonID string
	ExamVersion                        int32
	StartedAt                          time.Time
	DeadlineAt, SubmittedAt            *time.Time
	Score                              []byte
	LastSeq                            int64
	LastHash                           []byte
	// Set once the record's events were deleted (retention). The score stays.
	PurgedAt *time.Time
}

const attemptColumns = `id, assignment_id, exam_id, person_id, exam_version, started_at, deadline_at, submitted_at, score, last_seq, last_hash, purged_at`

func scanAttempt(row pgx.Row) (*attemptRow, error) {
	var a attemptRow
	err := row.Scan(&a.ID, &a.AssignmentID, &a.ExamID, &a.PersonID, &a.ExamVersion, &a.StartedAt, &a.DeadlineAt,
		&a.SubmittedAt, &a.Score, &a.LastSeq, &a.LastHash, &a.PurgedAt)
	return &a, err
}

func attemptBy(ctx context.Context, tx pgx.Tx, column, value string, lock bool) (*attemptRow, error) {
	if !validUUID(value) {
		return nil, notFound("attempt")
	}
	q := `SELECT ` + attemptColumns + ` FROM attempts WHERE ` + column + ` = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	a, err := scanAttempt(tx.QueryRow(ctx, q, value))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("attempt")
	}
	return a, err
}

// open refuses any change to an attempt whose record was deleted: its
// score stands, and nothing can be rebuilt from a log that is gone.
func (a *attemptRow) open() error {
	if a.PurgedAt != nil {
		return failedPrecondition("this attempt's record was deleted at the end of its retention period")
	}
	return nil
}

func (a *attemptRow) deadline() time.Time {
	if a.DeadlineAt == nil {
		return time.Time{}
	}
	return *a.DeadlineAt
}

// eventBody is what the chain hashes: the event as stored, deterministically
// encoded, so verification never depends on how a database renders JSON.
func eventBody(ev *v1.AttemptEvent) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(ev)
}

// appendEvents writes events to the chain. The caller holds the attempt row
// lock, so seq and prev_hash cannot race.
func (s *Server) appendEvents(ctx context.Context, tx pgx.Tx, a *attemptRow, evs []*v1.AttemptEvent) error {
	now := s.now()
	for _, ev := range evs {
		a.LastSeq++
		ev.Seq = a.LastSeq
		ev.ServerTime = now.UnixMilli()
		body, err := eventBody(ev)
		if err != nil {
			return err
		}
		hash := s.Keys.ChainHash(a.LastHash, body)
		if _, err := tx.Exec(ctx, `INSERT INTO attempt_events (tenant_id, attempt_id, seq, server_time, kind, body, hash)
			VALUES (am_tenant(), $1, $2, $3, $4, $5, $6)`, a.ID, ev.Seq, now, ev.GetKind(), body, hash); err != nil {
			return err
		}
		a.LastHash = hash
	}
	_, err := tx.Exec(ctx, `UPDATE attempts SET last_seq = $2, last_hash = $3 WHERE id = $1`, a.ID, a.LastSeq, a.LastHash)
	return err
}

// genesis is the chain's starting hash, bound to the attempt id so two
// attempts' chains can never be spliced together.
func (s *Server) genesis(attemptID string) []byte { return s.Keys.ChainHash(nil, []byte(attemptID)) }

// loadEvents reads the log and checks the chain as it goes.
func (s *Server) loadEvents(ctx context.Context, tx pgx.Tx, a *attemptRow) (events []*v1.AttemptEvent, intact bool, brokenAt int64, err error) {
	if a.PurgedAt != nil {
		// Deleted on purpose, with an audit record; not a broken chain.
		return nil, true, 0, nil
	}
	rows, err := tx.Query(ctx, `SELECT seq, body, hash FROM attempt_events WHERE attempt_id = $1 ORDER BY seq`, a.ID)
	if err != nil {
		return nil, false, 0, err
	}
	defer rows.Close()
	prev := s.genesis(a.ID)
	intact = true
	var expect int64 = 1
	for rows.Next() {
		var seq int64
		var body, hash []byte
		if err := rows.Scan(&seq, &body, &hash); err != nil {
			return nil, false, 0, err
		}
		var ev v1.AttemptEvent
		if err := proto.Unmarshal(body, &ev); err != nil {
			return nil, false, 0, err
		}
		ok := seq == expect && ev.GetSeq() == seq && secure.Equal(hash, s.Keys.ChainHash(prev, body))
		if !ok && intact {
			intact, brokenAt = false, seq
		}
		prev, expect = hash, seq+1
		events = append(events, &ev)
	}
	if err := rows.Err(); err != nil {
		return nil, false, 0, err
	}
	// A truncated log is a break too: the attempt row records how far it went.
	if intact && expect-1 != a.LastSeq {
		intact, brokenAt = false, expect
	}
	return events, intact, brokenAt, nil
}

func (s *Server) attemptProto(a *attemptRow, events []*v1.AttemptEvent, withEvents bool) (*v1.Attempt, error) {
	answers, flagged := domain.Fold(events, a.deadline())
	out := &v1.Attempt{
		Id: a.ID, AssignmentId: a.AssignmentID, ExamId: a.ExamID, ExamVersion: a.ExamVersion,
		StartedAt: a.StartedAt.UnixMilli(), DeadlineAt: msPtr(a.DeadlineAt), SubmittedAt: msPtr(a.SubmittedAt),
		Answers: answers, Flagged: flagged, LastSeq: a.LastSeq, PurgedAt: msPtr(a.PurgedAt),
	}
	if a.Score != nil {
		var sc v1.Score
		if err := protojson.Unmarshal(a.Score, &sc); err != nil {
			return nil, err
		}
		out.Score = &sc
	}
	if withEvents {
		out.Events = events
	}
	return out, nil
}
