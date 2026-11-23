-- Updates to the installation itself: which release a super administrator
-- asked for, and what the updater on the host did with it.
--
-- The core never replaces itself. It records the request; an updater on the
-- host (deploy/updater) claims it as the schema owner, backs the database
-- up, pulls the release's image by digest, migrates, restarts the core,
-- checks it is ready, and puts the previous image back if it is not. Each
-- step it finishes is written here, so the studio shows the same story the
-- host's log does.
--
-- Installation-wide, like super_admins: RLS on with no policy, and the
-- application role reaches it only through the functions below.

CREATE TABLE system_updates (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  from_version        text NOT NULL CHECK (length(from_version) <= 64),
  target_version      text NOT NULL CHECK (length(target_version) <= 64),
  -- Pinned by digest from the release's manifest, never a moving tag.
  image               text NOT NULL CHECK (image ~ '@sha256:[0-9a-f]{64}$'),
  psychometrics_image text NOT NULL DEFAULT '' CHECK (psychometrics_image = '' OR psychometrics_image ~ '@sha256:[0-9a-f]{64}$'),
  requested_by        text NOT NULL CHECK (length(requested_by) <= 320),
  requested_at        timestamptz NOT NULL DEFAULT now(),
  -- Wait for no sitting to be in progress before starting (the default), or
  -- start at once, for an urgent fix.
  wait_for_idle       boolean NOT NULL DEFAULT true,
  state               text NOT NULL DEFAULT 'requested'
                      CHECK (state IN ('requested', 'running', 'succeeded', 'failed', 'rolled_back', 'cancelled')),
  message             text NOT NULL DEFAULT '' CHECK (length(message) <= 2000),
  started_at          timestamptz,
  finished_at         timestamptz
);
-- One update at a time.
CREATE UNIQUE INDEX system_updates_one_active ON system_updates ((true)) WHERE state IN ('requested', 'running');
CREATE INDEX system_updates_recent ON system_updates (requested_at DESC);
ALTER TABLE system_updates ENABLE ROW LEVEL SECURITY;

-- The updater says it is alive each time it looks for work, so the studio
-- offers the button only where something will act on it.
CREATE TABLE system_updater (
  id      boolean PRIMARY KEY DEFAULT true CHECK (id),
  seen_at timestamptz NOT NULL,
  host    text NOT NULL DEFAULT '' CHECK (length(host) <= 200)
);
ALTER TABLE system_updater ENABLE ROW LEVEL SECURITY;

-- Sittings that an update would interrupt: started, not handed in, and still
-- inside their time (or, untimed, started in the last four hours). A count
-- only; says nothing about whose.
CREATE FUNCTION am_sittings_in_progress()
  RETURNS bigint
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT count(*) FROM attempts
        WHERE submitted_at IS NULL AND purged_at IS NULL
          AND (deadline_at > now() OR (deadline_at IS NULL AND started_at > now() - interval '4 hours')) $$;

-- Recent updates, newest first. The core calls it only for a super administrator.
CREATE FUNCTION am_updates(p_limit int)
  RETURNS TABLE (id uuid, from_version text, target_version text, requested_by text, requested_at timestamptz,
                 wait_for_idle boolean, state text, message text, started_at timestamptz, finished_at timestamptz)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT id, from_version, target_version, requested_by, requested_at, wait_for_idle, state, message, started_at, finished_at
        FROM system_updates ORDER BY requested_at DESC, id LIMIT least(p_limit, 50) $$;

-- When the updater last looked for work; null if it never has.
CREATE FUNCTION am_updater_seen()
  RETURNS timestamptz
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ SELECT seen_at FROM system_updater $$;

-- Asks for an update. Refused (unique_violation) while another is asked for
-- or running. The core checks the release against its feed first.
CREATE FUNCTION am_request_update(p_from text, p_target text, p_image text, p_psy text, p_by text, p_wait boolean)
  RETURNS uuid
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ INSERT INTO system_updates (from_version, target_version, image, psychometrics_image, requested_by, wait_for_idle)
        VALUES (p_from, p_target, p_image, p_psy, lower(p_by), p_wait) RETURNING id $$;

-- Withdraws a request the updater has not started. True if there was one.
CREATE FUNCTION am_cancel_update(p_by text)
  RETURNS boolean
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp
  AS $$ WITH c AS (UPDATE system_updates SET state = 'cancelled', finished_at = now(),
                     message = 'Cancelled by ' || lower(p_by)
                   WHERE state = 'requested' RETURNING 1)
        SELECT EXISTS (SELECT 1 FROM c) $$;

-- The updater's side, called as the owner by `core update`. Not granted to
-- the application role (db.Migrate revokes them): nothing a browser reaches
-- can claim or close an update.

-- Records that the updater is alive and hands it the update to run, if any:
-- one asked for and, unless it said not to wait, with no sitting in
-- progress. A run left 'running' for an hour is taken to have died with its
-- updater and is failed, so a later request is not blocked by it forever.
CREATE FUNCTION am_update_claim(p_host text)
  RETURNS TABLE (id uuid, from_version text, target_version text, image text, psychometrics_image text)
  LANGUAGE plpgsql VOLATILE SET search_path = public, pg_temp
  AS $$
  #variable_conflict use_column
  BEGIN
    INSERT INTO system_updater (id, seen_at, host) VALUES (true, now(), left(p_host, 200))
      ON CONFLICT (id) DO UPDATE SET seen_at = excluded.seen_at, host = excluded.host;
    UPDATE system_updates SET state = 'failed', finished_at = now(),
      message = 'The updater stopped before it finished; the server is on whichever version it was left on'
      WHERE state = 'running' AND started_at < now() - interval '1 hour';
    RETURN QUERY
      UPDATE system_updates u SET state = 'running', started_at = now(), message = 'Started'
      WHERE u.state = 'requested' AND (NOT u.wait_for_idle OR am_sittings_in_progress() = 0)
      RETURNING u.id, u.from_version, u.target_version, u.image, u.psychometrics_image;
  END $$;

-- The updater's progress and outcome. A finished update stays finished.
CREATE FUNCTION am_update_report(p_id uuid, p_state text, p_message text)
  RETURNS boolean
  LANGUAGE sql VOLATILE SET search_path = public, pg_temp
  AS $$ WITH r AS (UPDATE system_updates SET state = p_state, message = left(p_message, 2000),
                     finished_at = CASE WHEN p_state = 'running' THEN NULL ELSE now() END
                   WHERE id = p_id AND state = 'running' RETURNING 1)
        SELECT EXISTS (SELECT 1 FROM r) $$;

REVOKE ALL ON FUNCTION am_sittings_in_progress(), am_updates(int), am_updater_seen(),
  am_request_update(text, text, text, text, text, boolean), am_cancel_update(text),
  am_update_claim(text), am_update_report(uuid, text, text) FROM PUBLIC;
