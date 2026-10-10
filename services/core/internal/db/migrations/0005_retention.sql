-- Privacy track: retention and erasure.
--
-- An attempt's record (answers and integrity signals) is kept for the
-- tenant's retention period after the exam, then deleted; the candidate is
-- told so before they start (apps/studio/src/integrity.ts). What stays is
-- the attempt row with its score and the fact of the deletion, so results
-- and credentials keep working. Erasure on request deletes the row too.

-- At most 365: the consent text promises deletion within a year. A longer
-- period needs new consent text first.
ALTER TABLE tenants ADD COLUMN retention_days int NOT NULL DEFAULT 365
  CHECK (retention_days BETWEEN 30 AND 365);

ALTER TABLE attempts ADD COLUMN purged_at timestamptz;
ALTER TABLE attempts ADD COLUMN purged_why text CHECK (length(purged_why) <= 200);
CREATE INDEX attempts_retention_idx ON attempts (coalesce(submitted_at, deadline_at, started_at)) WHERE purged_at IS NULL;

-- attempt_events stays append-only: no update ever, and a delete only from
-- am_purge_attempt below (the table owner, with the purge flag it sets). The
-- application role has no DELETE on the table at all.
CREATE FUNCTION am_events_append_only() RETURNS trigger
  LANGUAGE plpgsql
  AS $$
  BEGIN
    IF TG_OP = 'DELETE' AND current_setting('am.purging', true) = 'on'
       AND current_user = (SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = TG_RELID) THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'insufficient_privilege';
  END $$;
DROP TRIGGER attempt_events_append_only ON attempt_events;
CREATE TRIGGER attempt_events_append_only BEFORE UPDATE OR DELETE ON attempt_events
  FOR EACH ROW EXECUTE FUNCTION am_events_append_only();

-- Delete one attempt's events and batch receipts. Scoped to the caller's
-- tenant like any request: an attempt of another tenant is not found.
CREATE FUNCTION am_purge_attempt(p_attempt uuid)
  RETURNS bigint
  LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$
  DECLARE n bigint;
  BEGIN
    IF am_tenant() IS NULL OR NOT EXISTS (SELECT 1 FROM attempts WHERE id = p_attempt AND tenant_id = am_tenant()) THEN
      RAISE EXCEPTION 'attempt not found' USING ERRCODE = 'no_data_found';
    END IF;
    PERFORM set_config('am.purging', 'on', true);
    DELETE FROM attempt_events WHERE attempt_id = p_attempt;
    GET DIAGNOSTICS n = ROW_COUNT;
    PERFORM set_config('am.purging', 'off', true);
    DELETE FROM attempt_batches WHERE attempt_id = p_attempt;
    RETURN n;
  END $$;

-- The retention job's one cross-tenant step: which records are past their
-- tenant's period at p_now. Ids only; the job then scopes itself per tenant.
CREATE FUNCTION am_retention_due(p_now timestamptz, p_limit int)
  RETURNS TABLE (tenant_id uuid, attempt_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$
    SELECT a.tenant_id, a.id FROM attempts a JOIN tenants t ON t.id = a.tenant_id
    WHERE a.purged_at IS NULL
      AND coalesce(a.submitted_at, a.deadline_at, a.started_at) < p_now - make_interval(days => t.retention_days)
    ORDER BY coalesce(a.submitted_at, a.deadline_at, a.started_at)
    LIMIT p_limit
  $$;

-- Mail is kept 30 days for delivery questions, then deleted with its
-- address. A body holding a link or code is cleared on hand-off already.
CREATE FUNCTION am_outbox_sweep(p_before timestamptz)
  RETURNS bigint
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$
    WITH gone AS (DELETE FROM outbox WHERE created_at < p_before RETURNING 1)
    SELECT count(*) FROM gone
  $$;
