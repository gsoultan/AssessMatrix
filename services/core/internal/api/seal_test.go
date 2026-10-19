package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// sitting is a started attempt with a few answers in its log.
func (h *harness) sitting(name string) (*client, *client, *v1.Attempt) {
	h.t.Helper()
	ctx := context.Background()
	staff := h.tenant(name)
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
		Events: []*v1.AttemptEvent{
			{Kind: "response.changed", ItemId: "q1", Values: []string{"a"}},
			{Kind: "response.changed", ItemId: "q2", Values: []string{"a", "c"}},
			{Kind: "response.typed", ItemId: "q3", Text: "Jakarta"},
			{Kind: "response.typed", ItemId: "q4", Text: "An essay."},
		}})))
	return staff, cand, att
}

// tamper runs statements as someone with the database owner's access, with
// the append-only trigger switched off as such an attacker would.
func (h *harness) tamper(stmts ...string) {
	h.t.Helper()
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, h.owner, func(tx pgx.Tx) error {
		for _, q := range append(append([]string{`ALTER TABLE attempt_events DISABLE TRIGGER attempt_events_append_only`}, stmts...),
			`ALTER TABLE attempt_events ENABLE TRIGGER attempt_events_append_only`) {
			if _, err := tx.Exec(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// Every way of rewriting an attempt's record, short of holding AM_SECRET, is
// detected: an edit (TestTamperedAttemptIsDetected), and each of these.
func TestEveryKindOfTamperingIsDetected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		stmts func(id string) []string
	}{
		// Remove the last events, and make the attempt row agree with what is left.
		{"cut off the end", func(id string) []string {
			return []string{
				`DELETE FROM attempt_events WHERE attempt_id = '` + id + `' AND seq > (SELECT last_seq - 2 FROM attempts WHERE id = '` + id + `')`,
				`UPDATE attempts a SET last_seq = e.seq, last_hash = e.hash FROM attempt_events e
					WHERE a.id = '` + id + `' AND e.attempt_id = a.id AND e.seq = a.last_seq - 2`,
			}
		}},
		// Remove an answer from the middle and close the gap.
		{"delete from the middle", func(id string) []string {
			return []string{
				`DELETE FROM attempt_events WHERE attempt_id = '` + id + `' AND kind = 'response.changed' AND body IS NOT NULL
					AND seq = (SELECT min(seq) FROM attempt_events WHERE attempt_id = '` + id + `' AND kind = 'response.changed')`,
			}
		}},
		// Swap two answers' places in the record.
		{"reorder", func(id string) []string {
			return []string{
				`UPDATE attempt_events e SET body = o.body, hash = o.hash FROM attempt_events o
					WHERE e.attempt_id = '` + id + `' AND o.attempt_id = e.attempt_id
					AND e.seq >= (SELECT last_seq - 1 FROM attempts WHERE id = e.attempt_id)
					AND o.seq = (SELECT 2 * last_seq - 1 FROM attempts WHERE id = e.attempt_id) - e.seq`,
			}
		}},
		// Make the whole record disappear as if retention had deleted it.
		{"fake a purge", func(id string) []string {
			return []string{
				`DELETE FROM attempt_events WHERE attempt_id = '` + id + `'`,
				`UPDATE attempts SET purged_at = now(), purged_why = 'retention' WHERE id = '` + id + `'`,
			}
		}},
		// Strip the seal, hoping for the treatment of a row from before seals.
		{"remove the seal", func(id string) []string {
			return []string{`UPDATE attempts SET seal = NULL WHERE id = '` + id + `'`}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staff, _, att := h.sitting("Tamper " + tc.name)
			before := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg
			if !before.GetChainIntact() {
				t.Fatal("an untouched record must check")
			}
			h.tamper(tc.stmts(att.GetId())...)
			log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg
			if log.GetChainIntact() {
				t.Fatal("the change must be detected")
			}
			if len(log.GetFlags()) == 0 || log.GetFlags()[0].GetType() != domain.FlagRecordAltered {
				t.Fatalf("a record_altered flag must lead, got %+v", log.GetFlags())
			}
		})
	}
}

// A record that no longer checks earns no credential, however good its score:
// the credential vouches for the record. And the candidate is shown why.
func TestAlteredRecordEarnsNoCredential(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff, cand, att := h.sitting("Forged pass")
	// The wrong answer to q1 becomes the right one, in place.
	var seq int64
	if err := h.owner.QueryRow(ctx, `SELECT seq FROM attempt_events WHERE attempt_id = $1 AND kind = 'response.changed' ORDER BY seq LIMIT 1`,
		att.GetId()).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	body, _ := eventBody(&v1.AttemptEvent{Seq: seq, Kind: "response.changed", ItemId: "q1", Values: []string{"b"}})
	h.tamper(fmt.Sprintf(`UPDATE attempt_events SET body = '\x%x' WHERE attempt_id = '%s' AND seq = %d`, body, att.GetId(), seq))

	sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()}))).Msg
	if !sub.GetOk() {
		t.Fatal("the hand-in itself must still be taken: the candidate's work is never refused")
	}
	reasons := sub.GetAttempt().GetFlagReasons()
	if len(reasons) == 0 || !strings.Contains(reasons[0], "segel") {
		t.Fatalf("the candidate must see the reason in Indonesian, got %q", reasons)
	}
	rev := get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 5}))).Msg
	if !rev.GetScore().GetPassed() {
		t.Fatalf("the test needs a passing score, got %+v", rev.GetScore())
	}
	if rev.GetCredentialUrl() != "" {
		t.Fatal("an altered record must not earn a credential")
	}
	var n int
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM credentials WHERE attempt_id = $1`, att.GetId()).Scan(&n); err != nil || n != 0 {
		t.Fatalf("credentials for the attempt = %d (%v), want none", n, err)
	}
	// And the same reasons when they come back to the link later.
	again := get(cand.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg.GetAttempt()
	if len(again.GetFlagReasons()) != len(reasons) {
		t.Fatalf("on return: %q, want %q", again.GetFlagReasons(), reasons)
	}
}

// Attempts from before rows were sealed are sealed by migrate when their
// chain checks; one already altered stays unsealed, and so reads as altered.
func TestMigrateSealsOnlyIntactLegacyAttempts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff, _, good := h.sitting("Legacy good")
	_, _, bad := h.sitting("Legacy bad")
	h.tamper(
		`UPDATE attempts SET seal = NULL WHERE id IN ('`+good.GetId()+`', '`+bad.GetId()+`')`,
		`UPDATE attempt_events SET body = body || '\x00'::bytea WHERE attempt_id = '`+bad.GetId()+`' AND seq = 3`,
	)
	if _, _, err := SealUnsealed(ctx, h.owner, h.srv.Keys); err != nil {
		t.Fatal(err)
	}
	if !get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: good.GetId()}))).Msg.GetChainIntact() {
		t.Fatal("an intact legacy attempt must be sealed and check")
	}
	var sealed bool
	if err := h.owner.QueryRow(ctx, `SELECT seal IS NOT NULL FROM attempts WHERE id = $1`, bad.GetId()).Scan(&sealed); err != nil || sealed {
		t.Fatalf("an altered legacy attempt must stay unsealed (sealed=%v, %v)", sealed, err)
	}
}
