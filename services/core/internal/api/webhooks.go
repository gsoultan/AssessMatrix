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

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/webhookdelivery"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/webhookendpoint"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/webhookevent"
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
func (s *Server) emit(ctx context.Context, tx *db.Tx, typ, person string, data map[string]any) error {
	rows, err := webhookendpoint.New().
		Where(webhookendpoint.Active.Eq(true), webhookendpoint.Events.Contains(typ)).
		Limit(maxWebhooksPerTenant).All(ctx, tx, nil)
	if err != nil {
		return err
	}
	endpoints := make([][16]byte, len(rows))
	for i, r := range rows {
		endpoints[i] = r.ID
	}
	if len(endpoints) == 0 {
		return nil
	}
	_, err = s.recordEvent(ctx, tx, typ, person, data, endpoints)
	return err
}

// recordEvent writes the event and one delivery per endpoint, returning the
// delivery ids in endpoint order.
func (s *Server) recordEvent(ctx context.Context, tx *db.Tx, typ, person string, data map[string]any, endpoints [][16]byte) ([]string, error) {
	id := newID()
	now := s.now().UTC()
	body, err := json.Marshal(envelope{ID: idText(id), Type: typ, CreatedAt: now.Format(time.RFC3339Nano), Data: data})
	if err != nil {
		return nil, fmt.Errorf("encode %s event: %w", typ, err)
	}
	ev := webhookevent.Create()
	ev.SetID(id)
	ev.SetTenantID(tx.Tenant())
	ev.SetType(typ)
	if person != "" {
		ev.SetPersonID(key(person))
	}
	ev.SetBody(string(body))
	ev.SetCreatedAt(now)
	if _, err := ev.Insert(ctx, tx); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		d := webhookdelivery.Create()
		d.SetTenantID(tx.Tenant())
		d.SetEndpointID(ep)
		d.SetEventID(id)
		d.SetCreatedAt(now)
		row, err := d.Insert(ctx, tx)
		if err != nil {
			return nil, err
		}
		did := idText(row.ID)
		if err := s.queueDelivery(ctx, tx, did); err != nil {
			return nil, err
		}
		out = append(out, did)
	}
	return out, nil
}

func (s *Server) queueDelivery(ctx context.Context, tx *db.Tx, id string) error {
	if s.Jobs == nil {
		return nil
	}
	_, err := s.Jobs.InsertTx(ctx, tx.Pgx(), webhook.DeliverArgs{DeliveryID: id}, webhook.InsertOpts())
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

// endpointOf is the API's view of an endpoint, with its delivery tallies.
func endpointOf(ctx context.Context, tx *db.Tx, r webhookendpoint.Row) (*v1.WebhookEndpoint, error) {
	ep := &v1.WebhookEndpoint{
		Id: idText(r.ID), Url: r.URL, Description: r.Description, Events: r.Events, Active: r.Active,
		CreatedAt: ms(r.CreatedAt), SecretHint: r.SecretHint,
	}
	of := webhookdelivery.New().Where(webhookdelivery.EndpointID.Eq(r.ID))
	delivered, err := of.Where(webhookdelivery.State.Eq(webhook.StateDelivered)).Count(ctx, tx)
	if err != nil {
		return nil, err
	}
	failing, err := of.Where(webhookdelivery.State.Eq(webhook.StateFailed)).Count(ctx, tx)
	if err != nil {
		return nil, err
	}
	last, ok, err := of.Where(webhookdelivery.LastTryAt.IsNotNull()).
		Order(webhookdelivery.LastTryAt.Desc()).One(ctx, tx)
	if err != nil {
		return nil, err
	}
	ep.Delivered, ep.Failing = int32(delivered), int32(failing)
	if ok {
		ep.LastDeliveryAt = msPtr(optTime(last.LastTryAt))
	}
	return ep, nil
}

func getEndpoint(ctx context.Context, tx *db.Tx, id string) (*v1.WebhookEndpoint, error) {
	r, err := endpointRow(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return endpointOf(ctx, tx, r)
}

func endpointRow(ctx context.Context, tx *db.Tx, id string) (webhookendpoint.Row, error) {
	if !validUUID(id) {
		return webhookendpoint.Row{}, notFound("webhook")
	}
	r, ok, err := webhookendpoint.New().IDEq(key(id)).One(ctx, tx)
	if err == nil && !ok {
		err = notFound("webhook")
	}
	return r, err
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		rows, err := webhookendpoint.New().Order(webhookendpoint.CreatedAt.Asc(), webhookendpoint.ID.Asc()).
			Limit(maxWebhooksPerTenant).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, r := range rows {
			ep, err := endpointOf(ctx, tx, r)
			if err != nil {
				return err
			}
			out.Endpoints = append(out.Endpoints, ep)
		}
		return nil
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
	err = s.tx(ctx, func(tx *db.Tx) error {
		// One count per tenant at a time, so two creates cannot both pass it.
		if err := lockTenant(ctx, tx); err != nil {
			return err
		}
		n, err := webhookendpoint.New().Count(ctx, tx)
		if err != nil {
			return err
		}
		if n >= maxWebhooksPerTenant {
			return failedPrecondition(fmt.Sprintf("at most %d webhooks; delete one first", maxWebhooksPerTenant))
		}
		uid := newID()
		id := idText(uid)
		sealed, err := s.Keys.Seal(webhook.SecretLabel(id), []byte(out.Secret))
		if err != nil {
			return err
		}
		ins := webhookendpoint.Create()
		ins.SetID(uid)
		ins.SetTenantID(tx.Tenant())
		ins.SetURL(url)
		ins.SetDescription(description)
		ins.SetEvents(events)
		ins.SetSecret(sealed)
		ins.SetSecretHint(out.Secret[:webhookSecretHintChars])
		if _, err := ins.Insert(ctx, tx); err != nil {
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
	err = s.tx(ctx, func(tx *db.Tx) error {
		r, err := endpointRow(ctx, tx, m.GetId())
		if err != nil {
			return err
		}
		mu := webhookendpoint.Mutate(r)
		mu.SetURL(url)
		mu.SetDescription(description)
		mu.SetEvents(events)
		mu.SetActive(m.GetActive())
		mu.SetUpdatedAtNow()
		if err := mu.Update(ctx, tx); err != nil {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		id := req.Msg.GetId()
		r, err := endpointRow(ctx, tx, id)
		if err != nil {
			return err
		}
		sealed, err := s.Keys.Seal(webhook.SecretLabel(id), []byte(out.Secret))
		if err != nil {
			return err
		}
		mu := webhookendpoint.Mutate(r)
		mu.SetSecret(sealed)
		mu.SetSecretHint(out.Secret[:webhookSecretHintChars])
		mu.SetUpdatedAtNow()
		if err := mu.Update(ctx, tx); err != nil {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		ep, err := endpointRow(ctx, tx, req.Msg.GetId())
		if err != nil {
			return err
		}
		// Its deliveries go with it (cascade); a queued job then finds
		// nothing and stops.
		if err := webhookendpoint.Delete(ctx, tx, ep.ID); err != nil {
			return err
		}
		return audit(ctx, tx, "webhook.deleted", idText(ep.ID), map[string]any{"host": hostOf(ep.URL)})
	})
	if err != nil {
		return nil, s.internal(ctx, "DeleteWebhook", err)
	}
	return connect.NewResponse(&v1.DeleteWebhookResponse{}), nil
}

// deliveriesOf is the API's view of deliveries, each with its event.
func deliveriesOf(ctx context.Context, tx *db.Tx, rows []webhookdelivery.Row) ([]*v1.WebhookDelivery, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([][16]byte, 0, len(rows))
	for _, r := range rows {
		if !slices.Contains(ids, r.EventID) {
			ids = append(ids, r.EventID)
		}
	}
	evs, err := webhookevent.New().IDIn(ids...).Limit(int64(len(ids))).All(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	byID := make(map[[16]byte]webhookevent.Row, len(evs))
	for _, e := range evs {
		byID[e.ID] = e
	}
	out := make([]*v1.WebhookDelivery, 0, len(rows))
	for _, r := range rows {
		e, ok := byID[r.EventID]
		if !ok {
			continue // erased while we read; the delivery goes with it
		}
		out = append(out, &v1.WebhookDelivery{
			Id: idText(r.ID), EndpointId: idText(r.EndpointID), EventId: idText(e.ID), EventType: e.Type,
			State: v1.DeliveryState(r.State), Tries: r.Tries, LastStatus: r.LastStatus, LastError: r.LastError,
			CreatedAt: ms(r.CreatedAt), LastTryAt: msPtr(optTime(r.LastTryAt)), DeliveredAt: msPtr(optTime(r.DeliveredAt)),
			Payload: e.Body,
		})
	}
	return out, nil
}

func getDelivery(ctx context.Context, tx *db.Tx, id string) (*v1.WebhookDelivery, error) {
	if !validUUID(id) {
		return nil, notFound("delivery")
	}
	r, ok, err := webhookdelivery.New().IDEq(key(id)).One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("delivery")
	}
	out, err := deliveriesOf(ctx, tx, []webhookdelivery.Row{r})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, notFound("delivery")
	}
	return out[0], nil
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		rows, err := webhookdelivery.New().
			WhereIf(m.GetEndpointId() != "", webhookdelivery.EndpointID.Eq(key(m.GetEndpointId()))).
			WhereIf(m.GetFailedOnly(), webhookdelivery.State.Eq(webhook.StateFailed)).
			Order(webhookdelivery.CreatedAt.Desc(), webhookdelivery.ID.Asc()).
			Limit(int64(limit)).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		out.Deliveries, err = deliveriesOf(ctx, tx, rows)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "ListDeliveries", err)
	}
	return connect.NewResponse(out), nil
}

func (s *webhookService) Redeliver(ctx context.Context, req *connect.Request[v1.RedeliverRequest]) (*connect.Response[v1.RedeliverResponse], error) {
	out := &v1.RedeliverResponse{}
	err := s.tx(ctx, func(tx *db.Tx) error {
		d, err := getDelivery(ctx, tx, req.Msg.GetDeliveryId())
		if err != nil {
			return err
		}
		// Same row, same event id and body: the receiver de-duplicates on it.
		mu := webhookdelivery.MutateKey(key(d.GetId()))
		mu.SetState(webhook.StatePending)
		mu.SetLastError("")
		if err := mu.Update(ctx, tx); err != nil {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		ep, err := getEndpoint(ctx, tx, req.Msg.GetId())
		if err != nil {
			return err
		}
		if !ep.GetActive() {
			return failedPrecondition("this webhook is paused; resume it first")
		}
		ids, err := s.recordEvent(ctx, tx, EventWebhookPing, "", map[string]any{"endpoint_id": ep.GetId()}, [][16]byte{key(ep.GetId())})
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
