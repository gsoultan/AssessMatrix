#!/usr/bin/env bash
# The whole stack on this machine, in one command, for development:
#
#   1. Postgres in Docker (compose.yaml's postgres service; data kept in its volume)
#   2. migrations, as the schema owner
#   3. a demo tenant and its first staff member (once)
#   4. the Go core, rebuilt and restarted whenever a .go or .sql file changes
#   5. the psychometrics worker, if python3 can import psycopg
#   6. the studio (and the candidate runner at /take/) and the verify page,
#      on Vite with hot reload, talking to the core
#
# Ctrl-C stops everything but Postgres; `make down` stops that too.
#
#   PORT=5180 VERIFY_PORT=5181 CORE_PORT=8080 METRICS_PORT=9090
#   AM_PG_PORT=5432 (in .env)   AM_DEMO_EMAIL=admin@example.id
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

CORE_PORT="${CORE_PORT:-8080}"
METRICS_PORT="${METRICS_PORT:-9090}"
VERIFY_PORT="${VERIFY_PORT:-5181}"
DEMO_EMAIL="${AM_DEMO_EMAIL:-admin@example.id}"
CORE="http://127.0.0.1:${CORE_PORT}"
DEV="$ROOT/.dev"
CORE_DIR="$ROOT/services/core"
mkdir -p "$DEV/media"

bold "AssessMatrix — the full stack"

# --- prerequisites -----------------------------------------------------------
need_bun
command -v go >/dev/null 2>&1 || die "go is not installed. On a Mac: brew install go"
command -v docker >/dev/null 2>&1 || die "docker is not installed. Install Docker Desktop or OrbStack, then start it"
docker info >/dev/null 2>&1 || die "docker is installed but not running. Start Docker Desktop (or OrbStack) and try again"
ensure_deps

port_busy() { lsof -ti tcp:"$1" -sTCP:LISTEN >/dev/null 2>&1; }
random_hex() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# --- .env: created on first run, with fresh secrets ---------------------------
ENV_FILE="$ROOT/.env"
if [ ! -f "$ENV_FILE" ]; then
  info "no .env yet — creating one from .env.example with fresh secrets"
  sed -e "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(random_hex)/" \
      -e "s/^AM_OWNER_PASSWORD=.*/AM_OWNER_PASSWORD=$(random_hex)/" \
      -e "s/^AM_APP_PASSWORD=.*/AM_APP_PASSWORD=$(random_hex)/" \
      -e "s/^AM_SECRET=.*/AM_SECRET=$(random_hex)/" \
      "$ROOT/.env.example" >"$ENV_FILE"
elif grep -q '^AM_SECRET=$' "$ENV_FILE"; then
  info "AM_SECRET is empty in .env — filling it"
  sed -i.bak "s/^AM_SECRET=$/AM_SECRET=$(random_hex)/" "$ENV_FILE" && rm -f "$ENV_FILE.bak"
fi
set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a
PG_PORT="${AM_PG_PORT:-5432}"
OWNER_URL="postgres://am_owner:${AM_OWNER_PASSWORD}@localhost:${PG_PORT}/assessmatrix"
APP_URL="postgres://am_app:${AM_APP_PASSWORD}@localhost:${PG_PORT}/assessmatrix"

# --- ports -------------------------------------------------------------------
for p in "$CORE_PORT" "$METRICS_PORT" "$VERIFY_PORT"; do
  port_busy "$p" && die "port $p is in use (pid $(lsof -ti tcp:"$p" -sTCP:LISTEN | tr '\n' ' ')). Stop it, or pick another: CORE_PORT= METRICS_PORT= VERIFY_PORT="
done
free_port # the studio's port, as `make dev` does

# --- 1. Postgres -------------------------------------------------------------
pg_running() { [ -n "$(cd "$ROOT" && docker compose ps -q --status running postgres 2>/dev/null)" ]; }
if ! pg_running && port_busy "$PG_PORT"; then
  die "port $PG_PORT is taken by another Postgres. Add AM_PG_PORT=5433 to .env and run again"
fi
info "starting Postgres on localhost:${PG_PORT}…"
(cd "$ROOT" && docker compose up -d --wait postgres) >/dev/null 2>&1 || {
  (cd "$ROOT" && docker compose logs --tail 30 postgres)
  die "Postgres did not start (see above)"
}
ok "Postgres ready"

# --- 2–3. migrate and seed ---------------------------------------------------
info "building the core…"
(cd "$CORE_DIR" && go build -o "$DEV/core" ./cmd/core) || die "the core does not build"
AM_MIGRATE_DATABASE_URL="$OWNER_URL" "$DEV/core" migrate >"$DEV/migrate.log" 2>&1 || {
  cat "$DEV/migrate.log"
  die "migrations failed. If you changed the passwords in .env after the first run, reset the database: make down && docker compose down -v"
}
ok "migrations applied"
if AM_MIGRATE_DATABASE_URL="$OWNER_URL" "$DEV/core" tenant "Demo" "$DEMO_EMAIL" >"$DEV/seed.log" 2>&1; then
  ok "seeded tenant \"Demo\" with staff $DEMO_EMAIL"
elif grep -q staff_email_key "$DEV/seed.log"; then
  ok "demo staff $DEMO_EMAIL already exists"
else
  cat "$DEV/seed.log"
  die "seeding the demo tenant failed"
fi

# --- the long-running processes ----------------------------------------------
PIDS=""
cleanup() {
  trap - INT TERM EXIT
  echo
  info "stopping…"
  for p in $PIDS; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  ok "stopped. Postgres is still running (data kept); \`make down\` stops it"
}
trap 'cleanup; exit 0' INT TERM
trap cleanup EXIT

# Prefix every line with its process, so four logs can share one terminal.
# Plain bash, not awk: some awks (mawk) buffer their input and show nothing.
tag() { local l; while IFS= read -r l; do printf '%s %s\n' "$1" "$l"; done; }

# Loopback only: with AM_DEV=1 the mailbox (and every sign-in code in it) is
# readable by anyone who can reach the port, and a laptop's port is the café's.
core_env() {
  exec env AM_DATABASE_URL="$APP_URL" AM_SECRET="$AM_SECRET" \
    AM_LISTEN="127.0.0.1:${CORE_PORT}" AM_METRICS_LISTEN="127.0.0.1:${METRICS_PORT}" \
    AM_PUBLIC_URL="http://localhost:${PORT}" AM_RUNNER_URL= \
    AM_DEV=1 AM_MAILER=mailbox AM_MEDIA_STORE=fs AM_MEDIA_DIR="$DEV/media" "$@"
}

# The core: build, migrate, serve; on any change under services/core, again.
core_loop() {
  # A supervisor outlives the failures it reports: no errexit in here.
  set +e
  local stamp="$DEV/core.stamp" serve=""
  trap '[ -n "$serve" ] && kill "$serve" 2>/dev/null; exit 0' TERM INT
  while true; do
    touch "$stamp"
    if [ -n "$serve" ]; then
      echo "change detected — rebuilding"
      if (cd "$CORE_DIR" && go build -o "$DEV/core.next" ./cmd/core) && mv "$DEV/core.next" "$DEV/core"; then
        kill "$serve" 2>/dev/null; wait "$serve" 2>/dev/null
        AM_MIGRATE_DATABASE_URL="$OWNER_URL" "$DEV/core" migrate || echo "migrations failed — fix them and save again"
      else
        echo "build failed — still serving the previous build; fix it and save again"
      fi
    fi
    if [ -z "$serve" ] || ! kill -0 "$serve" 2>/dev/null; then
      core_env "$DEV/core" serve &
      serve=$!
    fi
    # Poll: no watcher to install, and a second's latency is fine for a server.
    while [ -z "$(find "$CORE_DIR" -newer "$stamp" \( -name '*.go' -o -name '*.sql' -o -name 'go.mod' -o -name 'go.sum' \) -print -quit)" ]; do
      sleep 1
    done
  done
}
core_loop > >(tag "$(printf '\033[36m[core]\033[0m')") 2>&1 &
PIDS="$PIDS $!"

info "waiting for the core on :${CORE_PORT}…"
for _ in $(seq 1 60); do
  curl -sf "$CORE/readyz" -o /dev/null && break
  sleep 0.5
done
curl -sf "$CORE/readyz" -o /dev/null || die "the core did not become ready (see [core] above)"
ok "core ready"

if command -v python3 >/dev/null 2>&1 && python3 -c 'import psycopg' 2>/dev/null; then
  (cd "$ROOT/services/psychometrics" && AM_DATABASE_URL="$APP_URL" PYTHONDONTWRITEBYTECODE=1 exec python3 worker.py) \
    > >(tag "$(printf '\033[35m[psy]\033[0m ')") 2>&1 &
  PIDS="$PIDS $!"
else
  warn "skipping the psychometrics worker (item statistics): pip3 install -r services/psychometrics/requirements.txt to run it"
fi

# Vite's own binary under bun, so the pid we hold is the server itself.
(cd "$ROOT/apps/verify" && CORE_URL="$CORE" exec bun node_modules/vite/bin/vite.js --strictPort --port "$VERIFY_PORT") \
  > >(tag "$(printf '\033[33m[verify]\033[0m')") 2>&1 &
PIDS="$PIDS $!"
(cd "$APP" && VITE_BACKEND=core CORE_URL="$CORE" exec bun node_modules/vite/bin/vite.js --strictPort --port "$PORT") \
  > >(tag "$(printf '\033[32m[studio]\033[0m')") 2>&1 &
PIDS="$PIDS $!"
wait_for_server

echo
bold "AssessMatrix is running"
echo "  Studio          http://localhost:${PORT}           sign in as ${DEMO_EMAIL}"
echo "  Sign-in code    http://localhost:${PORT}/dev/mailbox?to=${DEMO_EMAIL}"
echo "  Candidate app   http://localhost:${PORT}/take/       (open a link from the mailbox)"
echo "  Verify page     http://localhost:${VERIFY_PORT}/verify/"
echo "  Core API        http://localhost:${CORE_PORT}         metrics on :${METRICS_PORT}"
echo "  Postgres        postgres://am_owner@localhost:${PG_PORT}/assessmatrix (passwords in .env)"
echo
info "edit the studio or verify page: the browser updates. Edit the core: it rebuilds and restarts."
info "Ctrl-C to stop."
wait
