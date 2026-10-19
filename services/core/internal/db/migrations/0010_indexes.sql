-- Indexes for reads that so far scanned a table.
--
-- attempts by person: erasure, and the check before deleting someone.
CREATE INDEX attempts_person_idx ON attempts (person_id);
-- media by uploader: retention and erasure delete a sitting's images.
CREATE INDEX media_uploader_idx ON media (tenant_id, uploaded_by);
-- The retention sweeps delete by age across every tenant.
CREATE INDEX outbox_created_idx ON outbox (created_at);
CREATE INDEX webhook_events_created_idx ON webhook_events (created_at);
-- The roster lists newest first.
CREATE INDEX people_tenant_created_idx ON people (tenant_id, created_at DESC);
