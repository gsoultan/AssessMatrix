// Package webhook delivers what the webhook log holds. Event rows are written
// in the same transaction as the change that caused them, with one delivery
// row and River job per subscribed endpoint; the worker signs the stored body
// with the endpoint's secret, posts it, and records how the receiver answered.
//
// The receiver is a URL a tenant typed in, so every request goes through a
// dialer that refuses private, loopback and link-local addresses at connect
// time (after DNS, so a name that resolves inward is refused too), follows no
// redirects and reads nothing back but the status.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/riverqueue/river"

	"github.com/gsoultan/storm"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/webhookdelivery"
)

// Delivery states, as stored.
const (
	StatePending   int16 = 1
	StateDelivered int16 = 2
	StateFailed    int16 = 3
)

// Queue keeps slow receivers from holding up mail and retention.
const Queue = "webhooks"

// MaxTries spans about four hours with the backoff below (attempt⁴ seconds).
const MaxTries = 10

// Timeout is how long a receiver has to answer one request.
const Timeout = 10 * time.Second

// Header names on every request.
const (
	HeaderSignature = "AssessMatrix-Signature"
	HeaderEventID   = "AssessMatrix-Event-Id"
	HeaderEventType = "AssessMatrix-Event-Type"
	HeaderDelivery  = "AssessMatrix-Delivery-Id"
)

// DeliverArgs is the River job: deliver one row.
type DeliverArgs struct {
	DeliveryID string `json:"delivery_id"`
}

func (DeliverArgs) Kind() string { return "webhook.deliver" }

// InsertOpts is how every delivery job is queued.
func InsertOpts() *river.InsertOpts {
	return &river.InsertOpts{Queue: Queue, MaxAttempts: MaxTries}
}

// SecretLabel binds a sealed secret to its endpoint row.
func SecretLabel(endpointID string) string { return "webhook:" + endpointID }

// Sign returns the signature header value for body at t.
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Verify is what a receiver does, here for tests and as the reference.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) bool {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return false
	}
	want := Sign(secret, time.Unix(sec, 0), body)
	return hmac.Equal([]byte(want), []byte("t="+ts+",v1="+sig))
}

// Policy says which receiver URLs are allowed.
type Policy struct {
	// AllowPrivate permits http and private addresses: development only, so
	// a local receiver can be pointed at (config refuses it without AM_DEV).
	AllowPrivate bool
}

// CheckURL refuses a URL a receiver cannot safely be. Names are checked
// again at connect time, when they have resolved.
func (p Policy) CheckURL(raw string) error {
	if len(raw) > 2000 {
		return errors.New("the URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errors.New("enter a full URL, like https://example.com/hooks/assessmatrix")
	}
	if u.User != nil {
		return errors.New("the URL must not carry a user name or password; verify the signature instead")
	}
	if u.Fragment != "" {
		return errors.New("the URL must not have a #fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowPrivate {
			return errors.New("the URL must be https")
		}
	default:
		return errors.New("the URL must be https")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		if !p.AllowPrivate {
			return errors.New("the URL must be reachable on the internet, not a local address")
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil && !p.AllowPrivate && !Public(ip) {
		return errors.New("the URL must be reachable on the internet, not a private or local address")
	}
	return nil
}

var blocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32", "fc00::/7",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Public reports whether ip is an ordinary internet address: not loopback,
// private, link-local (cloud metadata lives there), multicast or reserved.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// NewClient is the only HTTP client that talks to receivers.
func NewClient(p Policy) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !p.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !Public(ap.Addr()) {
				return fmt.Errorf("refused to connect to %s: not a public address", address)
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: Timeout,
		Transport: &http.Transport{
			// No proxy from the environment: it would dial on our behalf and
			// the address check above would see only the proxy.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: Timeout,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       90 * time.Second,
		},
		// A redirect is answered as a failure: following it would send the
		// signed body somewhere nobody configured.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Worker drains one delivery per job.
type Worker struct {
	river.WorkerDefaults[DeliverArgs]
	DB     *db.DB
	Keys   *secure.Keys
	Client *http.Client
}

func (w *Worker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	return Deliver(ctx, w.DB, w.Keys, w.Client, job.Args.DeliveryID, job.Attempt >= job.MaxAttempts)
}

// NextRetry spaces tries out from a second to about two hours.
func (w *Worker) NextRetry(job *river.Job[DeliverArgs]) time.Time {
	n := job.Attempt
	if n < 1 {
		n = 1
	}
	return time.Now().Add(time.Duration(n*n*n*n) * time.Second)
}

// Timeout bounds a whole job; the client's own timeout is the real limit.
func (w *Worker) Timeout(*river.Job[DeliverArgs]) time.Duration { return Timeout + 20*time.Second }

type row struct {
	endpointID, url, eventID, eventType, body string
	active                                    bool
	state                                     int16
	sealed                                    []byte
}

// Deliver posts one delivery if it is still pending. last says this is the
// final try, so a failure marks the row failed rather than pending. No
// transaction is held open across the request: a slow receiver costs a
// worker slot, never a database connection.
func Deliver(ctx context.Context, d *db.DB, keys *secure.Keys, client *http.Client, id string, last bool) error {
	key, err := storm.ParseUUID(id)
	if err != nil {
		return river.JobCancel(fmt.Errorf("webhook delivery %q: %w", id, err))
	}
	var owner queries.TenantOf
	err = d.Unscoped(ctx, func(tx *db.Tx) (err error) {
		owner, _, err = queries.WebhookDeliveryTenant.One(ctx, tx, key)
		return err
	})
	t, found := owner.TenantID.Get()
	if err != nil || !found {
		// Deleted with its endpoint, or swept: nothing left to send.
		return river.JobCancel(fmt.Errorf("webhook delivery %s not found: %w", id, err))
	}
	tenant := storm.UUID(t).String()
	var rows []webhookdelivery.ForSendRow
	err = d.InTenant(ctx, db.TenantID(tenant), func(tx *db.Tx) (err error) {
		rows, err = webhookdelivery.New().IDEq(key).Limit(1).AllForSend(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return river.JobCancel(fmt.Errorf("webhook delivery %s not found", id))
	}
	f := rows[0]
	r := row{
		endpointID: storm.UUID(f.EndpointID).String(), url: f.URL, active: f.Active, sealed: f.Secret,
		eventID: storm.UUID(f.EventID).String(), eventType: f.EventType, body: f.Body, state: f.State,
	}
	if r.state != StatePending {
		return nil // delivered already, or given up on and not asked again
	}
	if !r.active {
		return record(ctx, d, tenant, id, StateFailed, 0, "the endpoint is paused", false)
	}
	secret, err := keys.Open(SecretLabel(r.endpointID), r.sealed)
	if err != nil {
		return river.JobCancel(fmt.Errorf("open webhook secret for %s: %w", r.endpointID, err))
	}

	status, sendErr := post(ctx, client, r, id, string(secret))
	switch {
	case sendErr == nil:
		return record(ctx, d, tenant, id, StateDelivered, status, "", true)
	case last:
		if err := record(ctx, d, tenant, id, StateFailed, status, sendErr.Error(), true); err != nil {
			return err
		}
	default:
		if err := record(ctx, d, tenant, id, StatePending, status, sendErr.Error(), true); err != nil {
			return err
		}
	}
	return fmt.Errorf("webhook %s: %w", id, sendErr)
}

func post(ctx context.Context, client *http.Client, r row, deliveryID, secret string) (int, error) {
	body := []byte(r.body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, strings.NewReader(r.body))
	if err != nil {
		return 0, errors.New("the URL cannot be requested")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AssessMatrix-Webhooks/1")
	req.Header.Set(HeaderEventID, r.eventID)
	req.Header.Set(HeaderEventType, r.eventType)
	req.Header.Set(HeaderDelivery, deliveryID)
	req.Header.Set(HeaderSignature, Sign(secret, time.Now(), body))
	res, err := client.Do(req)
	if err != nil {
		return 0, errors.New(describe(err))
	}
	// Drain a little so the connection can be reused; the body is never kept.
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res.StatusCode, fmt.Errorf("the receiver answered %d", res.StatusCode)
	}
	return res.StatusCode, nil
}

// describe turns a transport error into a reason a person can act on,
// without echoing anything the receiver sent.
func describe(err error) string {
	var ne net.Error
	switch {
	case strings.Contains(err.Error(), "not a public address"):
		return "refused: the URL resolves to a private or local address"
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Sprintf("no answer within %s", Timeout)
	case strings.Contains(err.Error(), "no such host"):
		return "the host name does not resolve"
	case strings.Contains(err.Error(), "connection refused"):
		return "the connection was refused"
	case strings.Contains(err.Error(), "certificate"), strings.Contains(err.Error(), "tls:"):
		return "the TLS certificate was not accepted"
	}
	return "the request failed before the receiver answered"
}

func record(ctx context.Context, d *db.DB, tenant, id string, state int16, status int, why string, tried bool) error {
	if len(why) > 300 {
		why = why[:300]
	}
	key, err := storm.ParseUUID(id)
	if err != nil {
		return err
	}
	return d.InTenant(ctx, db.TenantID(tenant), func(tx *db.Tx) error {
		// Only a pending row moves: a concurrent redelivery that already
		// succeeded is not turned back into a failure. The row lock makes
		// the check and the write one step.
		cur, ok, err := webhookdelivery.New().IDEq(key).ForUpdate().One(ctx, tx)
		if err != nil || !ok || cur.State != StatePending {
			return err
		}
		m := webhookdelivery.Mutate(cur)
		m.SetState(state)
		m.SetLastStatus(int32(status))
		m.SetLastError(why)
		if tried {
			m.IncTries()
			m.SetLastTryAtNow()
		}
		if state == StateDelivered {
			m.SetDeliveredAtNow()
		}
		return m.Update(ctx, tx)
	})
}
