// Package mail sends what the outbox holds. Rows are written in the same
// transaction as the change that caused them, with a River job inserted
// alongside; the worker hands the message to a provider, marks it sent and
// clears the body, which is the only place a live link or code is at rest.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
)

// Message is one email.
type Message struct {
	ID, To, Subject, Body, Kind string
}

// Sender hands a message to a provider. Send must be safe to call twice for
// the same ID: the worker retries, and a provider may have accepted the first.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SendArgs is the River job: send one outbox row.
type SendArgs struct {
	OutboxID string `json:"outbox_id"`
}

func (SendArgs) Kind() string { return "mail.send" }

// Worker drains one outbox row per job.
type Worker struct {
	river.WorkerDefaults[SendArgs]
	DB     *db.DB
	Sender Sender
}

func (w *Worker) Work(ctx context.Context, job *river.Job[SendArgs]) error {
	return Deliver(ctx, w.DB, w.Sender, job.Args.OutboxID)
}

// Deliver sends one outbox row if it has not been sent. Idempotent.
func Deliver(ctx context.Context, d *db.DB, s Sender, id string) error {
	var tenant string
	err := d.Unscoped(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT am_outbox_tenant($1)::text`, id).Scan(&tenant)
	})
	if err != nil || tenant == "" {
		return river.JobCancel(fmt.Errorf("outbox row %s not found: %w", id, err))
	}
	return d.InTenant(ctx, db.TenantID(tenant), func(tx pgx.Tx) error {
		var m Message
		var sent *time.Time
		err := tx.QueryRow(ctx, `SELECT id, kind, to_addr, subject, body, sent_at FROM outbox WHERE id = $1 FOR UPDATE`, id).
			Scan(&m.ID, &m.Kind, &m.To, &m.Subject, &m.Body, &sent)
		if err != nil {
			return err
		}
		if sent != nil {
			return nil
		}
		if err := s.Send(ctx, m); err != nil {
			return fmt.Errorf("send %s: %w", id, err)
		}
		_, err = tx.Exec(ctx, `UPDATE outbox SET sent_at = now(), body = '' WHERE id = $1`, id)
		return err
	})
}

// Log writes messages to the log with links and codes redacted. For a
// deployment with no provider configured yet; nothing is delivered.
type Log struct{ L *slog.Logger }

func (l Log) Send(_ context.Context, m Message) error {
	l.L.Warn("mail not delivered: no provider configured", "id", m.ID, "kind", m.Kind, "to", m.To, "subject", redact(m.Subject))
	return nil
}

func redact(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			r = '•'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Mailbox keeps messages in memory for development and the browser test,
// which needs to read the link and code a candidate would receive. Never
// enabled in production: the config refuses it unless AM_DEV is set.
type Mailbox struct {
	mu   sync.Mutex
	msgs []Message
}

const mailboxKept = 200

func (b *Mailbox) Send(_ context.Context, m Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append([]Message{m}, b.msgs...)
	if len(b.msgs) > mailboxKept {
		b.msgs = b.msgs[:mailboxKept]
	}
	return nil
}

func (b *Mailbox) List() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Message{}, b.msgs...)
}

// SMTP sends through a relay with STARTTLS required.
type SMTP struct {
	Addr, From, User, Pass string
}

func (s SMTP) Send(ctx context.Context, m Message) error {
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return err
	}
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("header injection refused")
	}
	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, host)
	}
	msg := "From: " + s.From + "\r\n" +
		"To: " + m.To + "\r\n" +
		"Subject: " + m.Subject + "\r\n" +
		"Message-ID: <" + m.ID + "@assessmatrix>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(m.Body, "\n", "\r\n")
	done := make(chan error, 1)
	go func() { done <- smtp.SendMail(s.Addr, auth, s.From, []string{m.To}, []byte(msg)) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
