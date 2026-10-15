package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/webhook"
)

// Webhook event types. Payloads carry ids, versions, times and scores only:
// no names, emails or answers leave in a webhook.
const (
	EventExamPublished       = "exam.published"
	EventAssignmentInvited   = "assignment.invited"
	EventAttemptStarted      = "attempt.started"
	EventAttemptSubmitted    = "attempt.submitted"
	EventAttemptExpired      = "attempt.expired"
	EventAttemptScored       = "attempt.scored"
	EventCredentialIssued    = "credential.issued"
	EventCredentialRevoked   = "credential.revoked"
	EventWebhookPing         = "webhook.ping"
	maxWebhooksPerTenant     = 10
	maxDeliveriesListed      = 200
	defaultDeliveriesListed  = 50
	webhookSecretHintChars   = 10
	maxWebhookDescriptionLen = 200
)

// EventTypes is every type an endpoint can subscribe to, in display order.
var EventTypes = []string{
	EventExamPublished, EventAssignmentInvited, EventAttemptStarted, EventAttemptSubmitted,
	EventAttemptExpired, EventAttemptScored, EventCredentialIssued, EventCredentialRevoked,
}

// WebhookKeep is how long the event and delivery log is kept.
const WebhookKeep = 30 * 24 * time.Hour

type envelope struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	CreatedAt string         `json:"created_at"`
	Data      map[string]any `json:"data"`
}

// emit records an event for every active endpoint subscribed to typ, in the
// caller's transaction, and queues its deliveries. With no subscriber it
// costs one indexed read and writes nothing. person is the person the event
// is about ("" if none), kept so erasure can find the event.
func (s *Server) emit(ctx context.Context, tx pgx.Tx, typ, person string, data map[string]any) error {
	rows, err := tx.Query(ctx, `SELECT id::text FROM webhook_endpoints WHERE active AND $1 = ANY(events)`, typ)
	if err != nil {
		return err
	}
	endpoints, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return nil
	}
	_, err = s.recordEvent(ctx, tx, typ, person, data, endpoints)
	return err
}

// recordEvent writes the event and one delivery per endpoint, returning the
// delivery ids in endpoint order.
func (s *Server) recordEvent(ctx context.Context, tx pgx.Tx, typ, person string, data map[string]any, endpoints []string) ([]string, error) {
	var id string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	body, err := json.Marshal(envelope{ID: id, Type: typ, CreatedAt: now.Format(time.RFC3339Nano), Data: data})
	if err != nil {
		return nil, fmt.Errorf("encode %s event: %w", typ, err)
	}
	var pid *string
	if person != "" {
		pid = &person
	}
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_events (id, tenant_id, type, person_id, body, created_at)
		VALUES ($1, am_tenant(), $2, $3, $4, $5)`, id, typ, pid, string(body), now); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		var did string
		if err := tx.QueryRow(ctx, `INSERT INTO webhook_deliveries (tenant_id, endpoint_id, event_id, created_at)
			VALUES (am_tenant(), $1, $2, $3) RETURNING id`, ep, id, now).Scan(&did); err != nil {
			return nil, err
		}
		if err := s.queueDelivery(ctx, tx, did); err != nil {
			return nil, err
		}
		out = append(out, did)
	}
	return out, nil
}

func (s *Server) queueDelivery(ctx context.Context, tx pgx.Tx, id string) error {
	if s.Jobs == nil {
		return nil
	}
	_, err := s.Jobs.InsertTx(ctx, tx, webhook.DeliverArgs{DeliveryID: id}, webhook.InsertOpts())
	return err
}

// Event payloads, one shape per subject, so every event about an attempt
// reads the same.

func attemptData(att *attemptRow, score *v1.Score) map[string]any {
	d := map[string]any{
		"attempt_id": att.ID, "assignment_id": att.AssignmentID, "exam_id": att.ExamID,
		"exam_version": att.ExamVersion, "person_id": att.PersonID,
		"started_at": att.StartedAt.UTC().Format(time.RFC3339),
	}
	if att.DeadlineAt != nil {
		d["deadline_at"] = att.DeadlineAt.UTC().Format(time.RFC3339)
	}
	if att.SubmittedAt != nil {
		d["submitted_at"] = att.SubmittedAt.UTC().Format(time.RFC3339)
	}
	if score != nil {
		d["score"] = map[string]any{
			"pct": score.GetPct(), "points": score.GetRaw(), "max": score.GetMax(),
			"passed": score.GetPassed(), "pending_review": score.GetPendingHuman() > 0,
		}
	}
	return d
}

// webhookService is the staff API over endpoints and the delivery log.
type webhookService struct{ *Server }

const endpointColumns = `e.id::text, e.url, e.description, e.events, e.active, e.created_at, e.secret_hint,
	(SELECT count(*) FROM webhook_deliveries d WHERE d.endpoint_id = e.id AND d.state = 2)::int,
	(SELECT count(*) FROM webhook_deliveries d WHERE d.endpoint_id = e.id AND d.state = 3)::int,
	(SELECT max(d.last_try_at) FROM webhook_deliveries d WHERE d.endpoint_id = e.id)`

func scanEndpoint(r pgx.Row) (*v1.WebhookEndpoint, error) {
	var ep v1.WebhookEndpoint
	var created time.Time
	var last *time.Time
	if err := r.Scan(&ep.Id, &ep.Url, &ep.Description, &ep.Events, &ep.Active, &created, &ep.SecretHint,
		&ep.Delivered, &ep.Failing, &last); err != nil {
		return nil, err
	}
	ep.CreatedAt, ep.LastDeliveryAt = ms(created), msPtr(last)
	return &ep, nil
}

func getEndpoint(ctx context.Context, tx pgx.Tx, id string) (*v1.WebhookEndpoint, error) {
	if !validUUID(id) {
		return nil, notFound("webhook")
	}
	ep, err := scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoints e WHERE e.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("webhook")
	}
	return ep, err
}

// checkEndpoint validates what staff typed and normalises the event list.
func (s *webhookService) checkEndpoint(url, description string, events []string) (string, string, []string, error) {
	url, description = strings.TrimSpace(url), strings.TrimSpace(description)
	if err := s.WebhookPolicy.CheckURL(url); err != nil {
		return "", "", nil, invalidArg(err)
	}
	if len(description) > maxWebhookDescriptionLen {
		return "", "", nil, invalidArg(fmt.Errorf("the description can be at most %d characters", maxWebhookDescriptionLen))
	}
	var out []string
	for _, e := range events {
		if !slices.Contains(EventTypes, e) {
			return "", "", nil, invalidArg(fmt.Errorf("unknown event type %q", e))
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return "", "", nil, invalidArg(errors.New("choose at least one event"))
	}
	return url, description, out, nil
}

func newWebhookSecret() string { return "whsec_" + secure.Token() }

func (s *webhookService) ListWebhooks(ctx context.Context, _ *connect.Request[v1.ListWebhooksRequest]) (*connect.Response[v1.ListWebhooksResponse], error) {
	out := &v1.ListWebhooksResponse{EventTypes: EventTypes}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoints e ORDER BY e.created_at`)
		if err != nil {
			return err
		}
		out.Endpoints, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (*v1.WebhookEndpoint, error) { return scanEndpoint(r) })
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "ListWebhooks", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) CreateWebhook(ctx context.Context, req *connect.Request[v1.CreateWebhookRequest]) (*connect.Response[v1.CreateWebhookResponse], error) {
	url, description, events, err := s.checkEndpoint(req.Msg.GetUrl(), req.Msg.GetDescription(), req.Msg.GetEvents())
	if err != nil {
		return nil, err
	}
	out := &v1.CreateWebhookResponse{Secret: newWebhookSecret()}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		// One count per tenant at a time, so two creates cannot both pass it.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tenants WHERE id = am_tenant() FOR UPDATE`); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoints`).Scan(&n); err != nil {
			return err
		}
		if n >= maxWebhooksPerTenant {
			return failedPrecondition(fmt.Sprintf("at most %d webhooks; delete one first", maxWebhooksPerTenant))
		}
		var id string
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
			return err
		}
		sealed, err := s.Keys.Seal(webhook.SecretLabel(id), []byte(out.Secret))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_endpoints (id, tenant_id, url, description, events, secret, secret_hint)
			VALUES ($1, am_tenant(), $2, $3, $4, $5, $6)`, id, url, description, events, sealed, out.Secret[:webhookSecretHintChars]); err != nil {
			return err
		}
		if out.Endpoint, err = getEndpoint(ctx, tx, id); err != nil {
			return err
		}
		return audit(ctx, tx, "webhook.created", id, map[string]any{"host": hostOf(url), "events": events})
	})
	if err != nil {
		return nil, s.internal(ctx, "CreateWebhook", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) UpdateWebhook(ctx context.Context, req *connect.Request[v1.UpdateWebhookRequest]) (*connect.Response[v1.UpdateWebhookResponse], error) {
	m := req.Msg
	url, description, events, err := s.checkEndpoint(m.GetUrl(), m.GetDescription(), m.GetEvents())
	if err != nil {
		return nil, err
	}
	out := &v1.UpdateWebhookResponse{}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := getEndpoint(ctx, tx, m.GetId()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET url = $2, description = $3, events = $4, active = $5,
			updated_at = now() WHERE id = $1`, m.GetId(), url, description, events, m.GetActive()); err != nil {
			return err
		}
		if out.Endpoint, err = getEndpoint(ctx, tx, m.GetId()); err != nil {
			return err
		}
		return audit(ctx, tx, "webhook.updated", m.GetId(), map[string]any{"host": hostOf(url), "events": events, "active": m.GetActive()})
	})
	if err != nil {
		return nil, s.internal(ctx, "UpdateWebhook", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) RotateWebhookSecret(ctx context.Context, req *connect.Request[v1.RotateWebhookSecretRequest]) (*connect.Response[v1.RotateWebhookSecretResponse], error) {
	out := &v1.RotateWebhookSecretResponse{Secret: newWebhookSecret()}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		id := req.Msg.GetId()
		if _, err := getEndpoint(ctx, tx, id); err != nil {
			return err
		}
		sealed, err := s.Keys.Seal(webhook.SecretLabel(id), []byte(out.Secret))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET secret = $2, secret_hint = $3, updated_at = now() WHERE id = $1`,
			id, sealed, out.Secret[:webhookSecretHintChars]); err != nil {
			return err
		}
		if out.Endpoint, err = getEndpoint(ctx, tx, id); err != nil {
			return err
		}
		return audit(ctx, tx, "webhook.secret_rotated", id, nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "RotateWebhookSecret", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) DeleteWebhook(ctx context.Context, req *connect.Request[v1.DeleteWebhookRequest]) (*connect.Response[v1.DeleteWebhookResponse], error) {
	err := s.tx(ctx, func(tx pgx.Tx) error {
		ep, err := getEndpoint(ctx, tx, req.Msg.GetId())
		if err != nil {
			return err
		}
		// Its deliveries go with it (cascade); a queued job then finds
		// nothing and stops.
		if _, err := tx.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, ep.GetId()); err != nil {
			return err
		}
		return audit(ctx, tx, "webhook.deleted", ep.GetId(), map[string]any{"host": hostOf(ep.GetUrl())})
	})
	if err != nil {
		return nil, s.internal(ctx, "DeleteWebhook", err)
	}
	return connect.NewResponse(&v1.DeleteWebhookResponse{}), nil
}

const deliveryColumns = `d.id::text, d.endpoint_id::text, v.id::text, v.type, d.state, d.tries, d.last_status, d.last_error,
	d.created_at, d.last_try_at, d.delivered_at, v.body`

func scanDelivery(r pgx.Row) (*v1.WebhookDelivery, error) {
	var d v1.WebhookDelivery
	var state int16
	var created time.Time
	var tried, delivered *time.Time
	if err := r.Scan(&d.Id, &d.EndpointId, &d.EventId, &d.EventType, &state, &d.Tries, &d.LastStatus, &d.LastError,
		&created, &tried, &delivered, &d.Payload); err != nil {
		return nil, err
	}
	d.State = v1.DeliveryState(state)
	d.CreatedAt, d.LastTryAt, d.DeliveredAt = ms(created), msPtr(tried), msPtr(delivered)
	return &d, nil
}

func getDelivery(ctx context.Context, tx pgx.Tx, id string) (*v1.WebhookDelivery, error) {
	if !validUUID(id) {
		return nil, notFound("delivery")
	}
	d, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryColumns+`
		FROM webhook_deliveries d JOIN webhook_events v ON v.id = d.event_id WHERE d.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("delivery")
	}
	return d, err
}

func (s *webhookService) ListDeliveries(ctx context.Context, req *connect.Request[v1.ListDeliveriesRequest]) (*connect.Response[v1.ListDeliveriesResponse], error) {
	m := req.Msg
	limit := int(m.GetLimit())
	if limit <= 0 {
		limit = defaultDeliveriesListed
	}
	limit = min(limit, maxDeliveriesListed)
	if m.GetEndpointId() != "" && !validUUID(m.GetEndpointId()) {
		return nil, notFound("webhook")
	}
	out := &v1.ListDeliveriesResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var endpoint *string
		if m.GetEndpointId() != "" {
			e := m.GetEndpointId()
			endpoint = &e
		}
		rows, err := tx.Query(ctx, `SELECT `+deliveryColumns+`
			FROM webhook_deliveries d JOIN webhook_events v ON v.id = d.event_id
			WHERE ($1::uuid IS NULL OR d.endpoint_id = $1) AND (NOT $2 OR d.state = 3)
			ORDER BY d.created_at DESC, d.id LIMIT $3`, endpoint, m.GetFailedOnly(), limit)
		if err != nil {
			return err
		}
		out.Deliveries, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (*v1.WebhookDelivery, error) { return scanDelivery(r) })
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "ListDeliveries", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) Redeliver(ctx context.Context, req *connect.Request[v1.RedeliverRequest]) (*connect.Response[v1.RedeliverResponse], error) {
	out := &v1.RedeliverResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		d, err := getDelivery(ctx, tx, req.Msg.GetDeliveryId())
		if err != nil {
			return err
		}
		// Same row, same event id and body: the receiver de-duplicates on it.
		if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET state = 1, last_error = '' WHERE id = $1`, d.GetId()); err != nil {
			return err
		}
		if err := s.queueDelivery(ctx, tx, d.GetId()); err != nil {
			return err
		}
		if out.Delivery, err = getDelivery(ctx, tx, d.GetId()); err != nil {
			return err
		}
		return audit(ctx, tx, "webhook.redelivered", d.GetId(), map[string]any{"event": d.GetEventId()})
	})
	if err != nil {
		return nil, s.internal(ctx, "Redeliver", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) PingWebhook(ctx context.Context, req *connect.Request[v1.PingWebhookRequest]) (*connect.Response[v1.PingWebhookResponse], error) {
	out := &v1.PingWebhookResponse{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		ep, err := getEndpoint(ctx, tx, req.Msg.GetId())
		if err != nil {
			return err
		}
		if !ep.GetActive() {
			return failedPrecondition("this webhook is paused; resume it first")
		}
		ids, err := s.recordEvent(ctx, tx, EventWebhookPing, "", map[string]any{"endpoint_id": ep.GetId()}, []string{ep.GetId()})
		if err != nil {
			return err
		}
		out.Delivery, err = getDelivery(ctx, tx, ids[0])
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "PingWebhook", err)
	}
	return connect.NewResponse(out), nil
}

// hostOf is what the audit log keeps of a URL: a path may carry a token.
func hostOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}
