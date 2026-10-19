-- Super administrators: the people who run the whole installation.
--
-- A super administrator creates tenants and decides who administers each.
-- A tenant's administrators add users and give them access to the tenants
-- they administer, as members; they create no tenants and make no other
-- administrators.
--
-- It is a property of a person, not of one tenant, so it is kept by address
-- outside every tenant. A super administrator still signs in to a tenant as
-- any staff member does (setup makes them an administrator of the default
-- tenant); the session carries the flag with it.

CREATE TABLE super_admins (
  email      text PRIMARY KEY CHECK (email = lower(email) AND length(email) <= 320),
  created_at timestamptz NOT NULL DEFAULT now()
);
-- No policy: the application role reads and writes it only through the
-- functions below (db.Migrate also revokes it outright).
ALTER TABLE super_admins ENABLE ROW LEVEL SECURITY;

-- An installation set up before this migration: whoever completed setup, or
-- failing that the first administrator ever added, runs it from now on.
INSERT INTO super_admins (email)
SELECT e FROM (SELECT lower(coalesce(
  (SELECT detail->>'email' FROM audit_log WHERE action = 'setup.complete' ORDER BY at, id LIMIT 1),
  (SELECT email FROM staff WHERE role = 'admin' AND disabled_at IS NULL ORDER BY created_at, id LIMIT 1))) AS e) first
WHERE e IS NOT NULL;

-- Whether an address is a super administrator. Says nothing else.
CREATE FUNCTION am_super_admin(p_email text)
  RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT EXISTS (SELECT 1 FROM super_admins WHERE email = lower(p_email)) $$;

-- The session also says whether its holder is a super administrator, read on
-- every request like the role, so taking it away applies at once.
DROP FUNCTION am_staff_session(bytea);
CREATE FUNCTION am_staff_session(p_hash bytea)
  RETURNS TABLE (tenant_id uuid, staff_id uuid, role text, super_admin boolean)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT s.tenant_id, s.staff_id, st.role,
               EXISTS (SELECT 1 FROM super_admins sa WHERE sa.email = lower(st.email))
        FROM staff_sessions s JOIN staff st ON st.id = s.staff_id
        WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
          AND st.disabled_at IS NULL $$;

-- Every tenant. The core calls it only for a super administrator.
CREATE FUNCTION am_all_tenants()
  RETURNS TABLE (tenant_id uuid, tenant_name text, region text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT id, name, region FROM tenants ORDER BY lower(name), id $$;

-- The first person in an installation, from /setup or `core tenant`, is its
-- super administrator as well as the administrator of that first tenant.
-- Same signature as before, so its grants and revocations still apply.
CREATE OR REPLACE FUNCTION am_create_tenant(p_tenant text, p_region text, p_email text, p_name text)
  RETURNS TABLE (tenant_id uuid, staff_id uuid)
  LANGUAGE plpgsql VOLATILE SET search_path = public, pg_temp
  AS $$
  DECLARE t uuid; s uuid;
  BEGIN
    -- Bootstrapping only: an address that already works somewhere is given
    -- further tenants from the studio, by a super administrator.
    IF EXISTS (SELECT 1 FROM staff WHERE lower(email) = lower(p_email)) THEN
      RAISE EXCEPTION '% already has access to a tenant', p_email USING ERRCODE = 'unique_violation';
    END IF;
    INSERT INTO tenants (name, region) VALUES (p_tenant, coalesce(nullif(p_region, ''), 'id')) RETURNING id INTO t;
    INSERT INTO staff (tenant_id, email, name, role) VALUES (t, p_email, coalesce(p_name, ''), 'admin') RETURNING id INTO s;
    IF NOT EXISTS (SELECT 1 FROM super_admins) THEN
      INSERT INTO super_admins (email) VALUES (lower(p_email));
    END IF;
    RETURN QUERY SELECT t, s;
  END $$;

REVOKE ALL ON FUNCTION am_super_admin(text), am_staff_session(bytea), am_all_tenants() FROM PUBLIC;
