-- Images an instrument or an answer carries. The bytes are in object storage
-- under <tenant_id>/<id>; this row is what says who may read them.
CREATE TABLE media (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  content_type text NOT NULL CHECK (content_type IN ('image/webp', 'image/jpeg', 'image/png')),
  bytes        integer NOT NULL CHECK (bytes > 0 AND bytes <= 4194304),
  sha256       bytea NOT NULL CHECK (length(sha256) = 32),
  -- 'staff:<id>' or 'assignment:<id>': who put it there.
  uploaded_by  text NOT NULL CHECK (length(uploaded_by) <= 100),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_tenant_idx ON media (tenant_id);

ALTER TABLE media ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON media USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant());
