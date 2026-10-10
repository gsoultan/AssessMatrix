-- Phase 6: per-item scores for analysis, the analysis queue and its results.
--
-- item_scores is derived from the attempt log whenever an attempt is scored
-- (at hand-in and at every human review). The log stays the record; this is
-- an index of it shaped for statistics, rebuilt per attempt, never edited by
-- hand.
CREATE TABLE item_scores (
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  attempt_id   uuid NOT NULL REFERENCES attempts (id) ON DELETE CASCADE,
  exam_id      uuid NOT NULL REFERENCES exams (id) ON DELETE RESTRICT,
  exam_version int NOT NULL,
  item_id      text NOT NULL CHECK (length(item_id) <= 64),
  -- Null while an essay waits for a person.
  points       int CHECK (points >= 0),
  max_points   int NOT NULL CHECK (max_points >= 0),
  -- Choice ids picked, for distractor analysis.
  choices      text[] NOT NULL DEFAULT '{}',
  PRIMARY KEY (attempt_id, item_id)
);
CREATE INDEX item_scores_exam_idx ON item_scores (tenant_id, exam_id, exam_version);

-- One row per exam version whose statistics are stale. Written by the core,
-- taken by the psychometrics worker. Ids only.
CREATE TABLE analysis_queue (
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  exam_id      uuid NOT NULL REFERENCES exams (id) ON DELETE CASCADE,
  exam_version int NOT NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, exam_id, exam_version)
);

-- What the worker computed: the JSON form of GetItemAnalysisResponse.
CREATE TABLE item_stats (
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  exam_id      uuid NOT NULL REFERENCES exams (id) ON DELETE CASCADE,
  exam_version int NOT NULL,
  computed_at  timestamptz NOT NULL DEFAULT now(),
  stats        jsonb NOT NULL,
  PRIMARY KEY (tenant_id, exam_id, exam_version)
);

ALTER TABLE item_scores ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_scores USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant());
ALTER TABLE analysis_queue ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON analysis_queue USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant());
ALTER TABLE item_stats ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_stats USING (tenant_id = am_tenant()) WITH CHECK (tenant_id = am_tenant());

-- The worker's one cross-tenant step: take the oldest stale exam version,
-- returning only its ids. It is removed from the queue in the caller's
-- transaction, so a worker that fails puts it back by rolling back. The
-- caller then scopes itself to the tenant like any request.
CREATE FUNCTION am_take_analysis()
  RETURNS TABLE (tenant_id uuid, exam_id uuid, exam_version int)
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$
    DELETE FROM analysis_queue q
    WHERE (q.tenant_id, q.exam_id, q.exam_version) = (
      SELECT a.tenant_id, a.exam_id, a.exam_version FROM analysis_queue a
      ORDER BY a.requested_at LIMIT 1 FOR UPDATE SKIP LOCKED)
    RETURNING q.tenant_id, q.exam_id, q.exam_version
  $$;
