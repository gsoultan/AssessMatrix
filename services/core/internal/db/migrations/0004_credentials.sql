-- Phase 6: Open Badges 3.0 credentials.
--
-- Each tenant issues under its own RSA key (RS256, what every OB 3.0
-- verifier must accept). The private key is sealed with a key derived from
-- the server secret; only the public half is ever served.
CREATE TABLE issuer_keys (
  tenant_id   uuid NOT NULL REFERENCES tenants (id),
  kid         text NOT NULL CHECK (length(kid) BETWEEN 8 AND 64),
  public_jwk  jsonb NOT NULL,
  private_key bytea NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  -- Set when replaced: still served, so credentials it signed still verify.
  retired_at  timestamptz,
  PRIMARY KEY (tenant_id, kid)
);
CREATE UNIQUE INDEX issuer_keys_current ON issuer_keys (tenant_id) WHERE retired_at IS NULL;

-- One credential per attempt. The JWT is public by design: the candidate
-- shares its link. It names no one in the clear; the recipient is a salted
-- hash of their email.
CREATE TABLE credentials (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenants (id),
  attempt_id  uuid NOT NULL UNIQUE REFERENCES attempts (id) ON DELETE RESTRICT,
  jwt         text NOT NULL,
  issued_at   timestamptz NOT NULL DEFAULT now(),
  -- A later review took the result below the bar. Cleared if it passes again.
  revoked_at  timestamptz,
  revoked_why text
);
CREATE INDEX credentials_tenant_idx ON credentials (tenant_id);

ALTER TABLE issuer_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON issuer_keys USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant());
ALTER TABLE credentials ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credentials USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant());

-- Public reads for the verify page, by id only. Each returns what is already
-- public: a credential's JWT and status, an issuer's name and public keys.
CREATE FUNCTION am_public_credential(p_id uuid)
  RETURNS TABLE (jwt text, revoked_at timestamptz, revoked_why text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT c.jwt, c.revoked_at, c.revoked_why FROM credentials c WHERE c.id = p_id $$;

CREATE FUNCTION am_public_issuer(p_tenant uuid)
  RETURNS TABLE (name text, keys jsonb)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT t.name, coalesce((SELECT jsonb_agg(k.public_jwk ORDER BY k.created_at) FROM issuer_keys k WHERE k.tenant_id = t.id), '[]'::jsonb)
        FROM tenants t WHERE t.id = p_tenant $$;
