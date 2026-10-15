-- Outbound webhooks (docs/05-integration.md §4).
--
-- An event row is written in the same transaction as the change it reports,
-- with one delivery row and one River job per endpoint subscribed to it, so a
-- rolled-back change sends nothing and a committed one is delivered at least
-- once. The body is stored as the exact bytes that are signed and sent, so a
-- retry or a redelivery is byte-for-byte the original.

CREATE TABLE webhook_endpoints (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenants (id),
  url         text NOT NULL CHECK (length(url) BETWEEN 9 AND 2000),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 200),
  events      text[] NOT NULL CHECK (cardinality(events) BETWEEN 1 AND 32),
  active      boolean NOT NULL DEFAULT true,
  -- Sealed with the server's vault key (secure.Keys.Seal), bound to this id.
  secret      bytea NOT NULL,
  secret_hint text NOT NULL CHECK (length(secret_hint) <= 16),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhook_endpoints_tenant_idx ON webhook_endpoints (tenant_id) WHERE active;

CREATE TABLE webhook_events (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  type       text NOT NULL CHECK (length(type) <= 64),
  -- The person the event is about, if any, so erasure can find it. Payloads
  -- carry ids only, but an id of an erased person should not outlive them.
  person_id  uuid,
  body       text NOT NULL CHECK (length(body) <= 65536),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhook_events_tenant_idx ON webhook_events (tenant_id, created_at);
CREATE INDEX webhook_events_person_idx ON webhook_events (person_id) WHERE person_id IS NOT NULL;

CREATE TABLE webhook_deliveries (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  endpoint_id  uuid NOT NULL REFERENCES webhook_endpoints (id) ON DELETE CASCADE,
  event_id     uuid NOT NULL REFERENCES webhook_events (id) ON DELETE CASCADE,
  -- 1 pending, 2 delivered, 3 failed (out of tries).
  state        smallint NOT NULL DEFAULT 1 CHECK (state BETWEEN 1 AND 3),
  tries        int NOT NULL DEFAULT 0 CHECK (tries >= 0),
  last_status  int NOT NULL DEFAULT 0,
  last_error   text NOT NULL DEFAULT '' CHECK (length(last_error) <= 300),
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_try_at  timestamptz,
  delivered_at timestamptz,
  UNIQUE (endpoint_id, event_id)
);
CREATE INDEX webhook_deliveries_tenant_idx ON webhook_deliveries (tenant_id, created_at DESC);
CREATE INDEX webhook_deliveries_endpoint_idx ON webhook_deliveries (endpoint_id, created_at DESC);
CREATE INDEX webhook_deliveries_event_idx ON webhook_deliveries (event_id);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['webhook_endpoints', 'webhook_events', 'webhook_deliveries']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant())', t);
  END LOOP;
END $$;

-- The delivery worker finds a row before it knows the tenant.
CREATE FUNCTION am_webhook_delivery_tenant(p_id uuid)
  RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT d.tenant_id FROM webhook_deliveries d WHERE d.id = p_id $$;

-- The delivery log is kept 30 days, like mail, then deleted with its events.
CREATE FUNCTION am_webhook_sweep(p_before timestamptz)
  RETURNS bigint
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$
    WITH gone AS (DELETE FROM webhook_events WHERE created_at < p_before RETURNING 1)
    SELECT count(*) FROM gone
  $$;

REVOKE ALL ON FUNCTION am_webhook_delivery_tenant(uuid), am_webhook_sweep(timestamptz) FROM PUBLIC;
