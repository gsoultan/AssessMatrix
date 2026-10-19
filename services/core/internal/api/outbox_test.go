package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/mail"
)

// The outbox guarantee in README.md: mail goes out only if the change
// commits, and a link is at rest only until it is sent.

func TestMailIsQueuedOnlyIfTheChangeCommits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.tenant("Rollback")
	to := "rollback-" + h.lastTenant[:8] + "@example.test"

	boom := errors.New("the change failed")
	err := h.srv.DB.InTenant(ctx, db.TenantID(h.lastTenant), func(tx *db.Tx) error {
		if err := h.srv.queueMail(ctx, tx, "invite", to, "Undangan", "a link"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want the change's error back, got %v", err)
	}
	var n int
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE to_addr = $1`, to).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a rolled-back change left %d mail(s) in the outbox", n)
	}
}

type failingSender struct{}

func (failingSender) Send(context.Context, mail.Message) error { return errors.New("relay down") }

func TestSentInvitationLeavesNoLinkAtRest(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	f := h.publishedFixture(h.tenant("Outbox"))
	token := h.lastLink(f.email)
	var id string
	if err := h.owner.QueryRow(ctx, `SELECT id FROM outbox WHERE to_addr = $1 AND kind = 'invite'`, f.email).Scan(&id); err != nil {
		t.Fatal(err)
	}

	// A failed send keeps the body, so the retry still has the link.
	if err := mail.Deliver(ctx, h.srv.DB, failingSender{}, id); err == nil {
		t.Fatal("a failed send must report an error so the job retries")
	}
	if h.lastLink(f.email) != token {
		t.Fatal("a failed send must leave the message intact for the retry")
	}

	box := &mail.Mailbox{}
	for range 2 { // the worker may run twice for one row
		if err := mail.Deliver(ctx, h.srv.DB, box, id); err != nil {
			t.Fatal(err)
		}
	}
	got := box.List(f.email)
	if len(got) != 1 {
		t.Fatalf("delivering the same row twice must send once, sent %d", len(got))
	}
	if !strings.Contains(got[0].Body, token) {
		t.Fatal("the delivered invitation must carry the link")
	}

	var body string
	var sent bool
	if err := h.owner.QueryRow(ctx, `SELECT body, sent_at IS NOT NULL FROM outbox WHERE id = $1`, id).Scan(&body, &sent); err != nil {
		t.Fatal(err)
	}
	if !sent || body != "" {
		t.Fatalf("a sent row must be marked sent with its body cleared, sent=%v body=%q", sent, body)
	}
	// Nowhere else holds the link in the clear: only its keyed hash.
	var leaks int
	if err := h.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM outbox WHERE strpos(body, $1) > 0 OR strpos(subject, $1) > 0) +
		(SELECT count(*) FROM invite_events WHERE strpos(detail, $1) > 0) +
		(SELECT count(*) FROM audit_log WHERE strpos(audit_log::text, $1) > 0)`, token).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatalf("the link is still stored in the clear in %d row(s)", leaks)
	}
}
