-- The first schema. Forward-only: a change after this ships is a new file.
--
-- Tenancy: every business table carries tenant_id and has row-level security
-- keyed on the transaction-local setting app.tenant_id, which the core sets
-- from the authenticated principal and never from a request field. The core
-- connects as a role that owns nothing and cannot bypass RLS (checked at
-- startup), so a query that forgets its tenant sees nothing rather than
-- everything.
--
-- The few lookups that must cross tenants (a staff email at sign-in, a link
-- or session hash from a candidate) are SECURITY DEFINER functions that return
-- only the tenant and row id. The caller then scopes itself to that tenant.

CREATE FUNCTION am_tenant() RETURNS uuid
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE FUNCTION am_refuse_change() RETURNS trigger
  LANGUAGE plpgsql
  AS $$ BEGIN RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'insufficient_privilege'; END $$;

CREATE TABLE tenants (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  -- Data residency. PII and media for a tenant stay in this region.
  region     text NOT NULL DEFAULT 'id' CHECK (region ~ '^[a-z]{2}(-[a-z0-9]+)?$'),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE staff (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  email      text NOT NULL CHECK (email ~ '^[^@\s]+@[^@\s]+$' AND length(email) <= 320),
  name       text NOT NULL DEFAULT '' CHECK (length(name) <= 200),
  created_at timestamptz NOT NULL DEFAULT now()
);
-- One sign-in address maps to one tenant. Multi-tenant staff come with OIDC.
CREATE UNIQUE INDEX staff_email_key ON staff (lower(email));
CREATE INDEX staff_tenant_idx ON staff (tenant_id);

CREATE TABLE staff_codes (
  staff_id   uuid PRIMARY KEY REFERENCES staff (id) ON DELETE CASCADE,
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  code_hash  bytea NOT NULL,
  expires_at timestamptz NOT NULL,
  tries      int NOT NULL DEFAULT 0 CHECK (tries >= 0),
  sent_at    timestamptz NOT NULL
);
CREATE INDEX staff_codes_tenant_idx ON staff_codes (tenant_id);

CREATE TABLE staff_sessions (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  staff_id   uuid NOT NULL REFERENCES staff (id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz
);
CREATE INDEX staff_sessions_tenant_idx ON staff_sessions (tenant_id);
CREATE INDEX staff_sessions_staff_idx ON staff_sessions (staff_id);

CREATE TABLE exams (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES tenants (id),
  kind              smallint NOT NULL CHECK (kind BETWEEN 1 AND 3),
  status            smallint NOT NULL CHECK (status BETWEEN 1 AND 3),
  version           int NOT NULL DEFAULT 1 CHECK (version >= 1),
  published_version int NOT NULL DEFAULT 0 CHECK (published_version >= 0 AND published_version <= version),
  title             text NOT NULL CHECK (length(title) <= 300),
  issuer            text NOT NULL CHECK (length(issuer) <= 300),
  tier              smallint NOT NULL CHECK (tier BETWEEN 0 AND 3),
  pass_pct          smallint NOT NULL CHECK (pass_pct BETWEEN 0 AND 100),
  shuffle           boolean NOT NULL,
  timed             boolean NOT NULL,
  anonymous         boolean NOT NULL,
  -- Sections and items as protobuf JSON. Validated by the core on every save.
  sections          jsonb NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX exams_tenant_idx ON exams (tenant_id, updated_at DESC);

CREATE TABLE exam_versions (
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  exam_id      uuid NOT NULL REFERENCES exams (id) ON DELETE CASCADE,
  version      int NOT NULL,
  published_at timestamptz NOT NULL DEFAULT now(),
  note         text NOT NULL DEFAULT '',
  -- The whole Exam as protobuf JSON, frozen.
  snapshot     jsonb NOT NULL,
  PRIMARY KEY (exam_id, version)
);
CREATE INDEX exam_versions_tenant_idx ON exam_versions (tenant_id);

CREATE TABLE people (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  name       text NOT NULL CHECK (length(name) <= 200),
  email      text NOT NULL CHECK (email ~ '^[^@\s]+@[^@\s]+$' AND length(email) <= 320),
  grp        text NOT NULL DEFAULT '' CHECK (length(grp) <= 200),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX people_email_key ON people (tenant_id, lower(email));

CREATE TABLE assignments (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES tenants (id),
  exam_id        uuid NOT NULL REFERENCES exams (id) ON DELETE CASCADE,
  exam_version   int NOT NULL CHECK (exam_version >= 1),
  person_id      uuid NOT NULL REFERENCES people (id) ON DELETE CASCADE,
  assigned_at    timestamptz NOT NULL DEFAULT now(),
  due_at         timestamptz,
  status         smallint NOT NULL DEFAULT 1 CHECK (status BETWEEN 1 AND 4),
  started_at     timestamptz,
  submitted_at   timestamptz,
  score_pct      smallint CHECK (score_pct BETWEEN 0 AND 100),
  passed         boolean,
  -- Keyed hashes only. Nothing in this table can produce a working link.
  token_hash     bytea UNIQUE,
  sent_at        timestamptz,
  expires_at     timestamptz,
  redeemed_at    timestamptz,
  revoked_at     timestamptz,
  session_hash   bytea UNIQUE,
  otp_hash       bytea,
  otp_expires_at timestamptz,
  otp_tries      int NOT NULL DEFAULT 0 CHECK (otp_tries >= 0),
  otp_sent_at    timestamptz,
  otp_sends      int NOT NULL DEFAULT 0 CHECK (otp_sends >= 0),
  self_serves    int NOT NULL DEFAULT 0 CHECK (self_serves >= 0),
  UNIQUE (exam_id, person_id)
);
CREATE INDEX assignments_tenant_idx ON assignments (tenant_id, exam_id);
CREATE INDEX assignments_person_idx ON assignments (person_id);

-- Links an assignment used to have, so an old email can say it was replaced.
CREATE TABLE retired_tokens (
  token_hash    bytea PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants (id),
  assignment_id uuid NOT NULL REFERENCES assignments (id) ON DELETE CASCADE,
  retired_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX retired_tokens_assignment_idx ON retired_tokens (assignment_id, retired_at);
CREATE INDEX retired_tokens_tenant_idx ON retired_tokens (tenant_id);

-- What happened to an invitation, in order. Repeats fold into `count`.
CREATE TABLE invite_events (
  tenant_id     uuid NOT NULL REFERENCES tenants (id),
  assignment_id uuid NOT NULL REFERENCES assignments (id) ON DELETE CASCADE,
  seq           int NOT NULL,
  at            timestamptz NOT NULL DEFAULT now(),
  kind          text NOT NULL,
  detail        text NOT NULL DEFAULT '',
  count         int NOT NULL DEFAULT 1 CHECK (count >= 1),
  PRIMARY KEY (assignment_id, seq)
);
CREATE INDEX invite_events_tenant_idx ON invite_events (tenant_id);

CREATE TABLE attempts (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenants (id),
  -- RESTRICT: an attempt is evidence. Its exam and invitation cannot be
  -- deleted out from under it.
  assignment_id uuid NOT NULL UNIQUE REFERENCES assignments (id) ON DELETE RESTRICT,
  exam_id       uuid NOT NULL REFERENCES exams (id) ON DELETE RESTRICT,
  exam_version  int NOT NULL,
  person_id     uuid NOT NULL,
  started_at    timestamptz NOT NULL DEFAULT now(),
  deadline_at   timestamptz,
  submitted_at  timestamptz,
  score         jsonb,
  last_seq      bigint NOT NULL DEFAULT 0,
  last_hash     bytea NOT NULL
);
CREATE INDEX attempts_tenant_idx ON attempts (tenant_id, exam_id);

-- The attempt as an append-only, keyed hash chain:
-- hash = HMAC(key, prev_hash || body), where body is the deterministic
-- protobuf encoding of the event. Editing any row breaks every hash after it.
CREATE TABLE attempt_events (
  tenant_id   uuid NOT NULL REFERENCES tenants (id),
  attempt_id  uuid NOT NULL REFERENCES attempts (id) ON DELETE RESTRICT,
  seq         bigint NOT NULL CHECK (seq >= 1),
  server_time timestamptz NOT NULL,
  kind        text NOT NULL,
  body        bytea NOT NULL,
  hash        bytea NOT NULL,
  PRIMARY KEY (attempt_id, seq)
);
CREATE INDEX attempt_events_tenant_idx ON attempt_events (tenant_id);
CREATE TRIGGER attempt_events_append_only BEFORE UPDATE OR DELETE ON attempt_events
  FOR EACH ROW EXECUTE FUNCTION am_refuse_change();

-- Idempotency for AppendEvents: a retried batch is acknowledged, not re-appended.
CREATE TABLE attempt_batches (
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  attempt_id uuid NOT NULL REFERENCES attempts (id) ON DELETE RESTRICT,
  batch_id   text NOT NULL CHECK (length(batch_id) BETWEEN 1 AND 64),
  last_seq   bigint NOT NULL,
  PRIMARY KEY (attempt_id, batch_id)
);
CREATE INDEX attempt_batches_tenant_idx ON attempt_batches (tenant_id);

-- Every staff write, for whoever has to explain it later.
CREATE TABLE audit_log (
  id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants (id),
  at        timestamptz NOT NULL DEFAULT now(),
  actor     text NOT NULL,
  action    text NOT NULL,
  target    text NOT NULL DEFAULT '',
  detail    jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_tenant_idx ON audit_log (tenant_id, at DESC);
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION am_refuse_change();

-- Mail to send, written in the same transaction as the change that caused
-- it. The body holds a live link or code until it is handed to the provider,
-- then it is cleared: this is the only place either is ever at rest.
CREATE TABLE outbox (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES tenants (id),
  kind       text NOT NULL,
  to_addr    text NOT NULL,
  subject    text NOT NULL,
  body       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at    timestamptz
);
CREATE INDEX outbox_tenant_idx ON outbox (tenant_id);

-- Row-level security on every tenant table.
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['staff', 'staff_codes', 'staff_sessions', 'exams', 'exam_versions',
    'people', 'assignments', 'retired_tokens', 'invite_events', 'attempts', 'attempt_events',
    'attempt_batches', 'audit_log', 'outbox']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant())', t);
  END LOOP;
END $$;

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenants USING (id = am_tenant()) WITH CHECK (id = am_tenant());

-- Cross-tenant lookups. Each returns only ids; the caller scopes itself.

CREATE FUNCTION am_staff_by_email(p_email text)
  RETURNS TABLE (tenant_id uuid, staff_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT s.tenant_id, s.id FROM staff s WHERE lower(s.email) = lower(p_email) $$;

CREATE FUNCTION am_staff_session(p_hash bytea)
  RETURNS TABLE (tenant_id uuid, staff_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT s.tenant_id, s.staff_id FROM staff_sessions s
        WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now() $$;

-- current = false when the hash is a link this assignment used to have.
CREATE FUNCTION am_assignment_by_token(p_hash bytea)
  RETURNS TABLE (tenant_id uuid, assignment_id uuid, current boolean)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT * FROM (
          SELECT a.tenant_id, a.id, true FROM assignments a WHERE a.token_hash = p_hash
          UNION ALL
          SELECT r.tenant_id, r.assignment_id, false FROM retired_tokens r WHERE r.token_hash = p_hash
        ) hit ORDER BY 3 DESC LIMIT 1 $$;

CREATE FUNCTION am_assignment_by_session(p_hash bytea)
  RETURNS TABLE (tenant_id uuid, assignment_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT a.tenant_id, a.id FROM assignments a
        WHERE a.session_hash = p_hash AND a.revoked_at IS NULL $$;

-- Outbox rows are found by the mail worker before it knows the tenant.
CREATE FUNCTION am_outbox_tenant(p_id uuid)
  RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT o.tenant_id FROM outbox o WHERE o.id = p_id $$;

REVOKE ALL ON FUNCTION am_staff_by_email(text), am_staff_session(bytea),
  am_assignment_by_token(bytea), am_assignment_by_session(bytea), am_outbox_tenant(uuid) FROM PUBLIC;
