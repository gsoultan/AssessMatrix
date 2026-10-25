package model

// What leaves the system: mail and webhooks.

import (
	"time"

	"github.com/gsoultan/storm"
)

// Outbox is a mail waiting to be sent. It is written in the transaction that
// caused it; its body is cleared once sent.
type Outbox struct {
	ID        [16]byte
	Tenant    Tenant
	Kind      string
	ToAddr    string
	Subject   string
	Body      string
	CreatedAt time.Time
	SentAt    *time.Time
}

func (o *Outbox) Schema(t *storm.Table) {
	t.PrimaryKey(&o.ID)
	t.Name("outbox")
	t.Col(&o.ID).Default("gen_random_uuid()")
	t.Col(&o.Tenant).ConstraintName("outbox_tenant_id_fkey")
	t.Col(&o.CreatedAt).Default("now()")
	t.Index(&o.Tenant).Named("outbox_tenant_idx")
	t.Index(&o.CreatedAt).Named("outbox_created_idx")
}

// WebhookEndpoint is a receiver a tenant registered for some event types.
type WebhookEndpoint struct {
	storm.Model

	Tenant      Tenant
	URL         string
	Description string
	Events      []string
	Active      bool
	Secret      []byte
	SecretHint  string

	Deliveries []WebhookDelivery
}

func (w *WebhookEndpoint) Schema(t *storm.Table) {
	// What a child's (tenant_id, …_id) key points at (migration 0008_tenant_keys).
	t.UniqueNamed("webhook_endpoints_tenant_id_key", &w.Tenant, &w.ID)
	t.Col(&w.Tenant).ConstraintName("webhook_endpoints_tenant_id_fkey")
	t.Col(&w.Description).Default("''::text")
	t.Col(&w.Active).Default("true")
	t.Col(&w.Secret).NotNull()
	t.CheckNamed("webhook_endpoints_description_check", "length(description) <= 200")
	t.CheckNamed("webhook_endpoints_events_check", "(cardinality(events) >= 1) AND (cardinality(events) <= 32)")
	t.CheckNamed("webhook_endpoints_secret_hint_check", "length(secret_hint) <= 16")
	t.CheckNamed("webhook_endpoints_url_check", "(length(url) >= 9) AND (length(url) <= 2000)")
	t.Index(&w.Tenant).Where("active").Named("webhook_endpoints_tenant_idx")
}

// WebhookEvent is something that happened, as told to receivers.
type WebhookEvent struct {
	ID     [16]byte
	Tenant Tenant
	Type   string
	// PersonID is the person the event is about, if any. No foreign key:
	// the event outlives nothing, and erasure deletes it by this column.
	PersonID *[16]byte
	// SubjectID is the attempt an attempt.* event is about. Deliveries of
	// one subject to one endpoint go in Seq order (migration 0011).
	SubjectID *[16]byte
	Seq       int64
	Body      string
	CreatedAt time.Time

	Deliveries []WebhookDelivery
}

func (e *WebhookEvent) Schema(t *storm.Table) {
	t.PrimaryKey(&e.ID)
	// What a child's (tenant_id, …_id) key points at (migration 0008_tenant_keys).
	t.UniqueNamed("webhook_events_tenant_id_key", &e.Tenant, &e.ID)
	t.Col(&e.Tenant).ConstraintName("webhook_events_tenant_id_fkey")
	t.Col(&e.CreatedAt).Default("now()")
	t.Col(&e.Seq).Identity()
	t.CheckNamed("webhook_events_body_check", "length(body) <= 65536")
	t.CheckNamed("webhook_events_type_check", "length(type) <= 64")
	t.Index(&e.PersonID).Where("person_id IS NOT NULL").Named("webhook_events_person_idx")
	t.Index(&e.Tenant, &e.CreatedAt).Named("webhook_events_tenant_idx")
	t.Index(&e.CreatedAt).Named("webhook_events_created_idx")
	t.Index(&e.SubjectID, &e.Seq).Where("subject_id IS NOT NULL").Named("webhook_events_subject_idx")
}

// WebhookDelivery is one event on its way to one endpoint.
type WebhookDelivery struct {
	ID          [16]byte
	Tenant      Tenant
	Endpoint    WebhookEndpoint
	Event       WebhookEvent
	State       int16
	Tries       int32
	LastStatus  int32
	LastError   string
	CreatedAt   time.Time
	LastTryAt   *time.Time
	DeliveredAt *time.Time
}

func (d *WebhookDelivery) Schema(t *storm.Table) {
	t.PrimaryKey(&d.ID)
	t.Col(&d.ID).Default("gen_random_uuid()")
	t.Col(&d.Tenant).ConstraintName("webhook_deliveries_tenant_id_fkey")
	t.Col(&d.Endpoint).ConstraintName("webhook_deliveries_endpoint_id_fkey")
	t.Col(&d.Endpoint).OnDelete(storm.Cascade)
	t.Col(&d.Event).ConstraintName("webhook_deliveries_event_id_fkey")
	t.Col(&d.Event).OnDelete(storm.Cascade)
	t.Col(&d.State).Default("1")
	t.Col(&d.Tries).Default("0")
	t.Col(&d.LastStatus).Default("0")
	t.Col(&d.LastError).Default("''::text")
	t.Col(&d.CreatedAt).Default("now()")
	t.UniqueNamed("webhook_deliveries_endpoint_id_event_id_key", &d.Endpoint, &d.Event)
	t.CheckNamed("webhook_deliveries_last_error_check", "length(last_error) <= 300")
	t.CheckNamed("webhook_deliveries_state_check", "(state >= 1) AND (state <= 3)")
	t.CheckNamed("webhook_deliveries_tries_check", "tries >= 0")
	t.Index(&d.Endpoint, storm.Desc(&d.CreatedAt)).Named("webhook_deliveries_endpoint_idx")
	t.Index(&d.Event).Named("webhook_deliveries_event_idx")
	t.Index(&d.Tenant, storm.Desc(&d.CreatedAt)).Named("webhook_deliveries_tenant_idx")
}

// Joins declares the reads that cross from a delivery to what it delivers.
func (d *WebhookDelivery) Joins(j *storm.Joins) {
	var e WebhookEndpoint
	var v WebhookEvent
	// ForSend is everything the delivery job posts, in one read.
	j.Named("ForSend").
		Inner(&e, &d.Endpoint).
		Inner(&v, &d.Event).
		Take(&d.State, "State").
		Take(&e.ID, "EndpointID").
		Take(&e.URL, "URL").
		Take(&e.Active, "Active").
		Take(&e.Secret, "Secret").
		Take(&v.ID, "EventID").
		Take(&v.Type, "EventType").
		Take(&v.Body, "Body").
		Take(&v.SubjectID, "SubjectID").
		Take(&v.Seq, "Seq")
}
