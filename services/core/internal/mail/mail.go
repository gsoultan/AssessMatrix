// Package mail sends what the outbox holds. Rows are written in the same
// transaction as the change that caused them, with a River job inserted
// alongside; the worker hands the message to a provider, marks it sent and
// clears the body, which is the only place a live link or code is at rest.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/riverqueue/river"

	"github.com/gsoultan/storm"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/outbox"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
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
//
// No transaction is held open while the relay is talking: a slow relay costs
// a worker slot, never a database connection and a row lock. Two jobs racing
// on one row could both send, which Sender already allows for.
func Deliver(ctx context.Context, d *db.DB, s Sender, id string) error {
	key, err := storm.ParseUUID(id)
	if err != nil {
		return river.JobCancel(fmt.Errorf("outbox row %q: %w", id, err))
	}
	var tenant queries.TenantOf
	err = d.Unscoped(ctx, func(tx *db.Tx) (err error) {
		tenant, _, err = queries.OutboxTenant.One(ctx, tx, key)
		return err
	})
	t, found := tenant.TenantID.Get()
	if err != nil || !found {
		return river.JobCancel(fmt.Errorf("outbox row %s not found: %w", id, err))
	}
	scope := db.TenantID(storm.UUID(t).String())
	var r outbox.Row
	err = d.InTenant(ctx, scope, func(tx *db.Tx) error {
		var ok bool
		r, ok, err = outbox.New().IDEq(key).One(ctx, tx)
		if err == nil && !ok {
			err = fmt.Errorf("outbox row %s not found", id)
		}
		return err
	})
	if err != nil || r.SentAt.Valid {
		return err
	}
	m := Message{ID: id, Kind: r.Kind, To: r.ToAddr, Subject: r.Subject, Body: r.Body}
	if err := s.Send(ctx, m); err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	return d.InTenant(ctx, scope, func(tx *db.Tx) error {
		cur, ok, err := outbox.New().IDEq(key).ForUpdate().One(ctx, tx)
		if err != nil || !ok || cur.SentAt.Valid {
			return err
		}
		mu := outbox.MutateKey(key)
		mu.SetSentAtNow()
		mu.SetBody("")
		return mu.Update(ctx, tx)
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
	// Keep is how many messages are kept (0: 200). The load simulator
	// raises it (AM_DEV_MAILBOX_KEEP) to read a whole cohort's mail.
	Keep int
	mu   sync.Mutex
	msgs []Message // oldest first
}

func (b *Mailbox) Send(_ context.Context, m Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	keep := b.Keep
	if keep <= 0 {
		keep = 200
	}
	b.msgs = append(b.msgs, m)
	if len(b.msgs) > keep {
		b.msgs = append(b.msgs[:0:0], b.msgs[len(b.msgs)-keep:]...)
	}
	return nil
}

// List returns the messages to one address (all, if to is empty), newest first.
func (b *Mailbox) List(to string) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Message{}
	for i := len(b.msgs) - 1; i >= 0; i-- {
		if to == "" || strings.EqualFold(b.msgs[i].To, to) {
			out = append(out, b.msgs[i])
		}
	}
	return out
}

// SMTP sends through a relay with STARTTLS required (on port 587, or 25).
// The whole exchange is bounded by the context and by SMTPTimeout, so a relay
// that stops answering costs one job's slot for a bounded time, never a
// goroutine and a connection for good.
type SMTP struct {
	Addr, From, User, Pass string
}

// SMTPTimeout bounds one message's exchange with the relay when the caller's
// context has no earlier deadline.
const SMTPTimeout = 30 * time.Second

func (s SMTP) Send(ctx context.Context, m Message) error {
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return err
	}
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") || strings.ContainsAny(s.From, "\r\n") {
		return errors.New("header injection refused")
	}
	// AM_SMTP_FROM may carry a display name ("AssessMatrix <noreply@x>"):
	// the header keeps it, the envelope takes the bare address.
	from, err := netmail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("AM_SMTP_FROM: %w", err)
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return river.JobCancel(fmt.Errorf("recipient: %w", err))
	}
	ctx, cancel := context.WithTimeout(ctx, SMTPTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	// Closing the connection is what unblocks a read when the job is
	// cancelled before the deadline.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return errors.New("the SMTP relay does not offer STARTTLS; refusing to send in the clear")
	}
	if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Pass, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + s.From + "\r\n" +
		"To: " + m.To + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: <" + m.ID + "@assessmatrix>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(m.Body, "\n", "\r\n")
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
