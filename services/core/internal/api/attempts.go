package api

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attemptevent"
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
	// Keyed over the chain's head and the purge time (secure.AttemptSeal).
	Seal []byte
}

func attemptOf(r attempt.Row) *attemptRow {
	a := &attemptRow{
		ID: idText(r.ID), AssignmentID: idText(r.AssignmentID), ExamID: idText(r.ExamID), PersonID: idText(r.PersonID),
		ExamVersion: r.ExamVersion, StartedAt: r.StartedAt, DeadlineAt: optTime(r.DeadlineAt),
		SubmittedAt: optTime(r.SubmittedAt), LastSeq: r.LastSeq, LastHash: r.LastHash, PurgedAt: optTime(r.PurgedAt),
		Seal: r.Seal,
	}
	if sc, ok := r.Score.Get(); ok {
		a.Score = sc
	}
	return a
}

// attemptBy reads one attempt by its id or by its assignment's.
func attemptBy(ctx context.Context, tx *db.Tx, column, value string, lock bool) (*attemptRow, error) {
	if !validUUID(value) {
		return nil, notFound("attempt")
	}
	q := attempt.New()
	switch column {
	case "id":
		q = q.IDEq(key(value))
	case "assignment_id":
		q = q.AssignmentIDEq(key(value))
	default:
		return nil, errors.New("attemptBy: unknown column " + column)
	}
	if lock {
		q = q.ForUpdate()
	}
	r, ok, err := q.One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("attempt")
	}
	return attemptOf(r), nil
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
func (s *Server) appendEvents(ctx context.Context, tx *db.Tx, a *attemptRow, evs []*v1.AttemptEvent) error {
	now := s.now()
	id := key(a.ID)
	for _, ev := range evs {
		a.LastSeq++
		ev.Seq = a.LastSeq
		ev.ServerTime = now.UnixMilli()
		body, err := eventBody(ev)
		if err != nil {
			return err
		}
		hash := s.Keys.ChainHash(a.LastHash, body)
		n := attemptevent.Create()
		n.SetTenantID(tx.Tenant())
		n.SetAttemptID(id)
		n.SetSeq(ev.Seq)
		n.SetServerTime(now)
		n.SetKind(ev.GetKind())
		n.SetBody(body)
		n.SetHash(hash)
		if _, err := n.Insert(ctx, tx); err != nil {
			return err
		}
		a.LastHash = hash
	}
	a.Seal = s.seal(a)
	m := attempt.MutateKey(id)
	m.SetLastSeq(a.LastSeq)
	m.SetLastHash(a.LastHash)
	m.SetSeal(a.Seal)
	return m.Update(ctx, tx)
}

// seal is what the attempt row's seal should be for its current head.
func (s *Server) seal(a *attemptRow) []byte {
	var purged int64
	if a.PurgedAt != nil {
		purged = a.PurgedAt.UnixMilli()
	}
	return s.Keys.AttemptSeal(a.ID, a.LastSeq, a.LastHash, purged)
}

// genesis is the chain's starting hash, bound to the attempt id so two
// attempts' chains can never be spliced together.
func (s *Server) genesis(attemptID string) []byte { return s.Keys.ChainHash(nil, []byte(attemptID)) }

// loadEvents reads the log and checks it: every event against the one before
// it (the chain), and the attempt row against the chain's head (the seal).
// brokenAt is the first event that cannot be relied on.
func (s *Server) loadEvents(ctx context.Context, tx *db.Tx, a *attemptRow) (events []*v1.AttemptEvent, intact bool, brokenAt int64, err error) {
	sealed := secure.Equal(a.Seal, s.seal(a))
	if a.PurgedAt != nil && sealed {
		// Deleted on purpose, with an audit record; not a broken chain.
		return nil, true, 0, nil
	}
	// One past the cap, so a log longer than any attempt may write still
	// reads as more than the attempt row records, which is a break.
	rows, err := attemptevent.New().AttemptIDEq(key(a.ID)).Order(attemptevent.Seq.Asc()).
		Limit(MaxEventsPerTry+1).All(ctx, tx, nil)
	if err != nil {
		return nil, false, 0, err
	}
	prev := s.genesis(a.ID)
	intact = true
	var expect int64 = 1
	for _, r := range rows {
		seq, body, hash := r.Seq, r.Body, r.Hash
		var ev v1.AttemptEvent
		if err := proto.Unmarshal(body, &ev); err != nil {
			// Bytes no server wrote: a break, not a reason to refuse to read the rest.
			if intact {
				intact, brokenAt = false, seq
			}
			prev, expect = hash, seq+1
			continue
		}
		ok := seq == expect && ev.GetSeq() == seq && secure.Equal(hash, s.Keys.ChainHash(prev, body))
		if !ok && intact {
			intact, brokenAt = false, seq
		}
		prev, expect = hash, seq+1
		events = append(events, &ev)
	}
	// The chain checks, but does the row agree with where it ends? Events cut
	// off the end, or a purge marked by hand, break here: the seal covers the
	// count, the last hash and the purge time, and only the server can make one.
	if intact && (!sealed || expect-1 != a.LastSeq || !secure.Equal(prev, a.LastHash)) {
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
		Withdrawn: domain.Withdrawn(events),
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
