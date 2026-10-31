package api

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/assignment"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/person"
)

type proctorService struct{ *Server }

// The monitor polls rather than listens: a query every couple of seconds per
// watcher is nothing at pilot scale, needs no extra connection held per
// stream, and is easy to reason about. Revisit with LISTEN/NOTIFY when
// watchers number in the hundreds.
const (
	WatchEvery = 2 * time.Second
	// A stream ends after this and the studio reconnects, which re-checks the
	// staff session: a signed-out or removed staff member stops seeing data.
	WatchFor = 5 * time.Minute
	// Keep-alive for proxies that drop quiet connections.
	WatchKeepAlive = 20 * time.Second
	// Bound on what one stream tracks; an exam is a cohort, not a census.
	MaxSittings = 5000
	// An unchanged attempt is re-checked this often anyway: a row edited in
	// place does not grow the log, and the monitor must still show the break.
	WatchRecheck = time.Minute
)

// sittingCache keeps one attempt's derived view until its log grows or it is
// due a re-check, so a quiet attempt is not re-read on every tick.
type sittingCache struct {
	checkedAt time.Time
	lastSeq   int64
	answered  int32
	lastSeen  int64
	flags     []*v1.Flag
}

func (s *proctorService) WatchExam(ctx context.Context, req *connect.Request[v1.WatchExamRequest], stream *connect.ServerStream[v1.WatchExamResponse]) error {
	examID := req.Msg.GetExamId()
	if !validUUID(examID) {
		return notFound("instrument")
	}
	if err := s.tx(ctx, func(tx *db.Tx) error {
		_, err := getExam(ctx, tx, examID, false)
		return err
	}); err != nil {
		return s.internal(ctx, "WatchExam", err)
	}

	cache := map[string]*sittingCache{}
	versions := map[int32]*v1.Exam{}
	var sent []byte
	var sentAt time.Time
	end := s.now().Add(WatchFor)
	tick := time.NewTicker(WatchEvery)
	defer tick.Stop()
	for {
		snap, err := s.sittings(ctx, examID, cache, versions)
		if err != nil {
			return s.internal(ctx, "WatchExam", err)
		}
		body, err := proto.MarshalOptions{Deterministic: true}.Marshal(&v1.WatchExamResponse{Sittings: snap})
		if err != nil {
			return err
		}
		if !bytes.Equal(body, sent) || time.Since(sentAt) >= WatchKeepAlive {
			if err := stream.Send(&v1.WatchExamResponse{ServerNow: s.now().UnixMilli(), Sittings: snap}); err != nil {
				return nil // the watcher went away
			}
			sent, sentAt = body, time.Now()
		}
		if !s.now().Before(end) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.Stopping:
			return nil // the monitor reconnects to another replica
		case <-tick.C:
		}
	}
}

// sittings is everyone assigned the exam, with their attempt's progress and
// flags. Only attempts whose log grew since the last call are re-read.
func (s *proctorService) sittings(ctx context.Context, examID string, cache map[string]*sittingCache, versions map[int32]*v1.Exam) ([]*v1.Sitting, error) {
	var out []*v1.Sitting
	err := s.tx(ctx, func(tx *db.Tx) error {
		all, err := sittingRows(ctx, tx, examID)
		if err != nil {
			return err
		}

		live := map[string]bool{}
		for _, r := range all {
			v := r.version
			if r.attemptV != nil {
				v = *r.attemptV
			}
			e, ok := versions[v]
			if !ok {
				if e, err = examAt(ctx, tx, examID, v); err != nil {
					return err
				}
				versions[v] = e
			}
			r.sit.Total = int32(len(domain.AllItems(e)))
			if r.sit.AttemptId != "" {
				live[r.sit.AttemptId] = true
				r.sit.StartedAt = r.started.UnixMilli()
				if r.submitted != nil {
					r.sit.SubmittedAt = r.submitted.UnixMilli()
				}
				c := cache[r.sit.AttemptId]
				if c == nil || c.lastSeq != r.lastSeq || s.now().Sub(c.checkedAt) >= WatchRecheck {
					att, err := attemptBy(ctx, tx, "id", r.sit.AttemptId, false)
					if err != nil {
						return err
					}
					events, intact, brokenAt, err := s.loadEvents(ctx, tx, att)
					if err != nil {
						return err
					}
					answers, _ := domain.Fold(events, att.deadline())
					c = &sittingCache{checkedAt: s.now(), lastSeq: att.LastSeq, answered: int32(len(answers)),
						flags: flagsOut(domain.Flags(events, questionNumbers(e), intact, brokenAt))}
					if n := len(events); n > 0 {
						c.lastSeen = events[n-1].GetServerTime()
					}
					cache[r.sit.AttemptId] = c
				}
				r.sit.Answered, r.sit.LastSeenAt, r.sit.Flags = c.answered, c.lastSeen, c.flags
			}
			out = append(out, r.sit)
		}
		// Forget attempts no longer listed, so the cache stays bounded.
		for id := range cache {
			if !live[id] {
				delete(cache, id)
			}
		}
		return nil
	})
	return out, err
}

// sittingRow is one assignment of the exam, with its attempt if started.
type sittingRow struct {
	sit       *v1.Sitting
	version   int32
	attemptV  *int32
	lastSeq   int64
	started   *time.Time
	submitted *time.Time
	name      string
	email     string
}

// maxExamRows bounds each read behind the sittings list; MaxSittings then
// keeps the most recently started of them.
const maxExamRows = 50_000

// sittingRows is everyone assigned the exam, latest start first, then by name.
func sittingRows(ctx context.Context, tx *db.Tx, examID string) ([]sittingRow, error) {
	eid := key(examID)
	as, err := assignment.New().Where(assignment.ExamID.Eq(eid)).Limit(maxExamRows).All(ctx, tx, nil)
	if err != nil || len(as) == 0 {
		return nil, err
	}
	ats, err := attempt.New().Where(attempt.ExamID.Eq(eid)).Limit(maxExamRows).All(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	byAssignment := make(map[[16]byte]attempt.Row, len(ats))
	for _, t := range ats {
		byAssignment[t.AssignmentID] = t
	}
	pids := make([][16]byte, 0, len(as))
	for _, a := range as {
		pids = append(pids, a.PersonID)
	}
	ps, err := person.New().IDIn(pids...).Limit(int64(len(pids))).All(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	people := make(map[[16]byte]person.Row, len(ps))
	for _, p := range ps {
		people[p.ID] = p
	}
	all := make([]sittingRow, 0, len(as))
	for _, a := range as {
		p, ok := people[a.PersonID]
		if !ok {
			continue // removed from the roster while we read
		}
		r := sittingRow{
			sit: &v1.Sitting{AssignmentId: idText(a.ID), PersonName: cmp.Or(p.Name, p.Email),
				Status: v1.AssignStatus(a.Status), ScorePct: int32(a.ScorePct.V)},
			version: a.ExamVersion, name: p.Name, email: p.Email,
		}
		if t, ok := byAssignment[a.ID]; ok {
			r.sit.AttemptId = idText(t.ID)
			r.attemptV, r.lastSeq, r.started, r.submitted = &t.ExamVersion, t.LastSeq, &t.StartedAt, optTime(t.SubmittedAt)
		}
		all = append(all, r)
	}
	slices.SortStableFunc(all, func(x, y sittingRow) int {
		switch {
		case x.started != nil && y.started != nil:
			if c := y.started.Compare(*x.started); c != 0 {
				return c
			}
		case x.started != nil:
			return -1
		case y.started != nil:
			return 1
		}
		return cmp.Or(cmp.Compare(x.name, y.name), cmp.Compare(x.email, y.email))
	})
	return all[:min(len(all), MaxSittings)], nil
}
