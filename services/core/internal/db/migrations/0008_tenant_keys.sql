-- A row may point only at a row in its own tenant.
--
-- Foreign keys are checked without row-level security, so a key on the
-- parent's id alone accepts a child in tenant B naming a parent in tenant A.
-- Every handler checks first today, but one that forgot would let B write
-- rows that A's deletes cascade into (or are blocked by), and learn from the
-- error which of A's ids exist. Keying each reference on (tenant_id, id) makes
-- that impossible in the database, whatever the code above it does.
--
-- Pre-pilot tables are small, so this runs in the migration's transaction.
-- Validation fails the migration if any row already crosses a tenant: that
-- would be a breach to investigate, not something to migrate past.

ALTER TABLE staff ADD CONSTRAINT staff_tenant_id_key UNIQUE (tenant_id, id);
ALTER TABLE exams ADD CONSTRAINT exams_tenant_id_key UNIQUE (tenant_id, id);
ALTER TABLE people ADD CONSTRAINT people_tenant_id_key UNIQUE (tenant_id, id);
ALTER TABLE assignments ADD CONSTRAINT assignments_tenant_id_key UNIQUE (tenant_id, id);
ALTER TABLE attempts ADD CONSTRAINT attempts_tenant_id_key UNIQUE (tenant_id, id);
ALTER TABLE webhook_endpoints ADD CONSTRAINT webhook_endpoints_tenant_id_key UNIQUE (tenant_id, id);
ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_tenant_id_key UNIQUE (tenant_id, id);

-- Same names and the same ON DELETE as before; only the columns change.
ALTER TABLE staff_codes
  DROP CONSTRAINT staff_codes_staff_id_fkey,
  ADD CONSTRAINT staff_codes_staff_id_fkey FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE staff_sessions
  DROP CONSTRAINT staff_sessions_staff_id_fkey,
  ADD CONSTRAINT staff_sessions_staff_id_fkey FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE exam_versions
  DROP CONSTRAINT exam_versions_exam_id_fkey,
  ADD CONSTRAINT exam_versions_exam_id_fkey FOREIGN KEY (tenant_id, exam_id) REFERENCES exams (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE assignments
  DROP CONSTRAINT assignments_exam_id_fkey,
  ADD CONSTRAINT assignments_exam_id_fkey FOREIGN KEY (tenant_id, exam_id) REFERENCES exams (tenant_id, id) ON DELETE CASCADE,
  DROP CONSTRAINT assignments_person_id_fkey,
  ADD CONSTRAINT assignments_person_id_fkey FOREIGN KEY (tenant_id, person_id) REFERENCES people (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE retired_tokens
  DROP CONSTRAINT retired_tokens_assignment_id_fkey,
  ADD CONSTRAINT retired_tokens_assignment_id_fkey FOREIGN KEY (tenant_id, assignment_id) REFERENCES assignments (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE invite_events
  DROP CONSTRAINT invite_events_assignment_id_fkey,
  ADD CONSTRAINT invite_events_assignment_id_fkey FOREIGN KEY (tenant_id, assignment_id) REFERENCES assignments (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE attempts
  DROP CONSTRAINT attempts_exam_id_fkey,
  ADD CONSTRAINT attempts_exam_id_fkey FOREIGN KEY (tenant_id, exam_id) REFERENCES exams (tenant_id, id) ON DELETE RESTRICT,
  DROP CONSTRAINT attempts_assignment_id_fkey,
  ADD CONSTRAINT attempts_assignment_id_fkey FOREIGN KEY (tenant_id, assignment_id) REFERENCES assignments (tenant_id, id) ON DELETE RESTRICT;
ALTER TABLE attempt_events
  DROP CONSTRAINT attempt_events_attempt_id_fkey,
  ADD CONSTRAINT attempt_events_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempts (tenant_id, id) ON DELETE RESTRICT;
ALTER TABLE attempt_batches
  DROP CONSTRAINT attempt_batches_attempt_id_fkey,
  ADD CONSTRAINT attempt_batches_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempts (tenant_id, id) ON DELETE RESTRICT;
ALTER TABLE item_scores
  DROP CONSTRAINT item_scores_attempt_id_fkey,
  ADD CONSTRAINT item_scores_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempts (tenant_id, id) ON DELETE CASCADE,
  DROP CONSTRAINT item_scores_exam_id_fkey,
  ADD CONSTRAINT item_scores_exam_id_fkey FOREIGN KEY (tenant_id, exam_id) REFERENCES exams (tenant_id, id) ON DELETE RESTRICT;
ALTER TABLE analysis_queue
  DROP CONSTRAINT analysis_queue_exam_id_fkey,
  ADD CONSTRAINT analysis_queue_exam_id_fkey FOREIGN KEY (tenant_id, exam_id) REFERENCES exams (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE item_stats
  DROP CONSTRAINT item_stats_exam_id_fkey,
  ADD CONSTRAINT item_stats_exam_id_fkey FOREIGN KEY (tenant_id, exam_id) REFERENCES exams (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE credentials
  DROP CONSTRAINT credentials_attempt_id_fkey,
  ADD CONSTRAINT credentials_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempts (tenant_id, id) ON DELETE RESTRICT;
ALTER TABLE webhook_deliveries
  DROP CONSTRAINT webhook_deliveries_endpoint_id_fkey,
  ADD CONSTRAINT webhook_deliveries_endpoint_id_fkey FOREIGN KEY (tenant_id, endpoint_id) REFERENCES webhook_endpoints (tenant_id, id) ON DELETE CASCADE,
  DROP CONSTRAINT webhook_deliveries_event_id_fkey,
  ADD CONSTRAINT webhook_deliveries_event_id_fkey FOREIGN KEY (tenant_id, event_id) REFERENCES webhook_events (tenant_id, id) ON DELETE CASCADE;
