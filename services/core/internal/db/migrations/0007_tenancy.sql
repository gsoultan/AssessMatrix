-- Tenancy for staff: one person may work in several tenants, with a role in
-- each, and an instrument can be shared from one tenant into others.
--
-- A staff row stays what it was: one person's membership of one tenant, under
-- that tenant's row-level security. Belonging to three tenants is three rows
-- with the same address. Nothing here lets a query see two tenants at once;
-- moving between them mints a new session in the other tenant.

-- admin: everything a member can do, plus who has access, new tenants and
-- sharing instruments. member: the everyday work. Least privilege by default.
ALTER TABLE staff ADD COLUMN role text NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member'));
-- Access is withdrawn, not deleted: the audit log names staff by id, and a
-- deleted row would leave "staff:<id>" pointing at nobody.
ALTER TABLE staff ADD COLUMN disabled_at timestamptz;
-- Until now every staff member could do everything. Keep it so for them.
UPDATE staff SET role = 'admin';

DROP INDEX staff_email_key;
CREATE UNIQUE INDEX staff_email_key ON staff (tenant_id, lower(email));
CREATE INDEX staff_email_idx ON staff (lower(email));

-- Sign-in by address now has a choice of tenants: the one used last, else the
-- oldest membership. The others are a switch away.
CREATE OR REPLACE FUNCTION am_staff_by_email(p_email text)
  RETURNS TABLE (tenant_id uuid, staff_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT s.tenant_id, s.id FROM staff s
        WHERE lower(s.email) = lower(p_email) AND s.disabled_at IS NULL
        ORDER BY (SELECT max(ss.created_at) FROM staff_sessions ss WHERE ss.staff_id = s.id) DESC NULLS LAST,
                 s.created_at, s.id
        LIMIT 1 $$;

-- The session now also says the role, so an admin-only procedure is refused
-- before its handler runs. A withdrawn membership ends its sessions here too,
-- not only through the revocation that withdrawing writes.
DROP FUNCTION am_staff_session(bytea);
CREATE FUNCTION am_staff_session(p_hash bytea)
  RETURNS TABLE (tenant_id uuid, staff_id uuid, role text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT s.tenant_id, s.staff_id, st.role FROM staff_sessions s JOIN staff st ON st.id = s.staff_id
        WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
          AND st.disabled_at IS NULL $$;

-- Every tenant one address belongs to. The core calls it only with the
-- address of the staff member who is signed in, so it tells them nothing
-- about anyone else.
CREATE FUNCTION am_staff_tenants(p_email text)
  RETURNS TABLE (tenant_id uuid, staff_id uuid, tenant_name text, region text, role text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT s.tenant_id, s.id, t.name, t.region, s.role FROM staff s JOIN tenants t ON t.id = s.tenant_id
        WHERE lower(s.email) = lower(p_email) AND s.disabled_at IS NULL
        ORDER BY lower(t.name), t.id $$;

REVOKE ALL ON FUNCTION am_staff_session(bytea), am_staff_tenants(text) FROM PUBLIC;

-- Where a shared instrument came from. A copy is an ordinary instrument of
-- the tenant that holds it, with its own versions, invitations and attempts;
-- these columns only say which original it follows. No foreign key: the
-- original is another tenant's row, and deleting it must not reach in here.
ALTER TABLE exams ADD COLUMN source_exam_id uuid;
ALTER TABLE exams ADD COLUMN source_version int CHECK (source_version >= 1);
ALTER TABLE exams ADD COLUMN source_tenant_name text CHECK (length(source_tenant_name) <= 200);
ALTER TABLE exams ADD CONSTRAINT exams_source_whole CHECK (
  (source_exam_id IS NULL) = (source_version IS NULL) AND (source_exam_id IS NULL) = (source_tenant_name IS NULL));
-- One copy of an original per tenant: sharing again updates it.
CREATE UNIQUE INDEX exams_source_key ON exams (tenant_id, source_exam_id) WHERE source_exam_id IS NOT NULL;
-- Sharing copies an image into each tenant once: one it already holds, byte
-- for byte, is reused.
CREATE INDEX media_sha_idx ON media (tenant_id, sha256);
