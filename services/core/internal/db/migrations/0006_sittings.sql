-- Sittings: a person can sit an instrument again once their previous sitting
-- is closed (handed in, or its link ran out before it was used). Each sitting
-- is its own assignment row, with its own link, attempt and score; earlier
-- sittings are never touched.
--
-- The rule "at most one open sitting" depends on the clock (link expiry), so
-- it cannot be a partial index. AssignPeople enforces it under a row lock on
-- the latest sitting, and the unique key below catches two requests racing
-- for the same next number.
ALTER TABLE assignments ADD COLUMN sitting int NOT NULL DEFAULT 1 CHECK (sitting >= 1);
ALTER TABLE assignments ADD CONSTRAINT assignments_sitting_key UNIQUE (exam_id, person_id, sitting);
ALTER TABLE assignments DROP CONSTRAINT assignments_exam_id_person_id_key;
