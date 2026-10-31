-- Every batch of signals an attempt uploads checks the candidate's consents
-- first. Without this the check walked the attempt's whole log, so a long
-- exam paid more for each batch than the one before. Partial: consents are a
-- handful of rows per attempt, so the index stays tiny next to the log.
CREATE INDEX attempt_events_consent_idx ON attempt_events (attempt_id) WHERE kind LIKE 'consent.%';
