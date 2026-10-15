-- First-run setup (docs/13 §1): the first organisation and its administrator.
--
-- am_create_tenant is the one place a tenant and its first staff member are
-- made. `core tenant` calls it as the owner. The application role may not
-- (db.Migrate revokes it); the serving process reaches it only through
-- am_setup_first_tenant, which works once, on an empty database.

CREATE FUNCTION am_create_tenant(p_tenant text, p_region text, p_email text, p_name text)
  RETURNS TABLE (tenant_id uuid, staff_id uuid)
  LANGUAGE plpgsql VOLATILE SET search_path = public, pg_temp
  AS $$
  DECLARE t uuid; s uuid;
  BEGIN
    INSERT INTO tenants (name, region) VALUES (p_tenant, coalesce(nullif(p_region, ''), 'id')) RETURNING id INTO t;
    INSERT INTO staff (tenant_id, email, name) VALUES (t, p_email, coalesce(p_name, '')) RETURNING id INTO s;
    RETURN QUERY SELECT t, s;
  END $$;

-- True while there is no organisation at all. Says nothing else.
CREATE FUNCTION am_setup_needed()
  RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT NOT EXISTS (SELECT 1 FROM tenants) $$;

-- Creates the first organisation and administrator, and nothing after that:
-- the lock makes two setups racing on a fresh install end with one winner,
-- and the loser gets SQLSTATE AM001.
CREATE FUNCTION am_setup_first_tenant(p_tenant text, p_region text, p_email text, p_name text)
  RETURNS TABLE (tenant_id uuid, staff_id uuid)
  LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$
  DECLARE t uuid; s uuid;
  BEGIN
    LOCK TABLE tenants IN SHARE ROW EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM tenants) THEN
      RAISE EXCEPTION 'setup is already complete' USING ERRCODE = 'AM001';
    END IF;
    SELECT c.tenant_id, c.staff_id INTO t, s FROM am_create_tenant(p_tenant, p_region, p_email, p_name) c;
    INSERT INTO audit_log (tenant_id, actor, action, target, detail)
      VALUES (t, 'setup', 'setup.complete', 'staff:' || s, jsonb_build_object('email', p_email));
    RETURN QUERY SELECT t, s;
  END $$;

-- The newest migration applied, for the setup page. The application role
-- cannot read schema_migrations itself.
CREATE FUNCTION am_schema_version()
  RETURNS text
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT coalesce(max(name), '') FROM schema_migrations $$;

REVOKE ALL ON FUNCTION am_create_tenant(text, text, text, text), am_setup_needed(),
  am_setup_first_tenant(text, text, text, text), am_schema_version() FROM PUBLIC;
