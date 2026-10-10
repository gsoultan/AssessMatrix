#!/usr/bin/env bash
# The browser test against the real thing: the studio built for the core, the
# Go core serving it on one origin, Postgres underneath, mail in the dev mailbox.
#
#   AM_E2E_MIGRATE_DATABASE_URL  the schema owner (migrations, tenant creation)
#   AM_E2E_DATABASE_URL          the unprivileged application role
#
# Each run creates its own tenant, so it can share a database with other runs.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — e2e against the core"
ensure_deps
: "${AM_E2E_MIGRATE_DATABASE_URL:?set AM_E2E_MIGRATE_DATABASE_URL to the owner role}"
: "${AM_E2E_DATABASE_URL:?set AM_E2E_DATABASE_URL to the app role}"
CORE_PORT="${CORE_PORT:-8090}"
URL="http://localhost:${CORE_PORT}"
# The runner on its own origin, as production may put it. The test forwards
# this port to the core, so it can cut the candidate's network.
RUNNER_PORT="${RUNNER_PORT:-8091}"
# The phase 4 gate is a 5-minute outage; set it shorter for a quick local run.
OUTAGE_SECONDS="${OUTAGE_SECONDS:-300}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

info "building the studio for the core"
(cd "$APP" && VITE_BACKEND=core bunx vite build --outDir "$WORK/web" --emptyOutDir >/dev/null && VITE_BACKEND=core bunx vite build -c vite.runner.config.ts --outDir "$WORK/web" >/dev/null)

info "building the core"
(cd "$ROOT/services/core" && go build -o "$WORK/core" ./cmd/core)

export AM_MIGRATE_DATABASE_URL="$AM_E2E_MIGRATE_DATABASE_URL"
"$WORK/core" migrate >/dev/null
STAFF="e2e.$(date +%s).$$@example.id"
"$WORK/core" tenant "E2E $(date +%s)" "$STAFF" >/dev/null
unset AM_MIGRATE_DATABASE_URL

for p in "$CORE_PORT" "$RUNNER_PORT"; do lsof -ti tcp:"$p" 2>/dev/null | xargs kill -9 2>/dev/null || true; done
AM_DATABASE_URL="$AM_E2E_DATABASE_URL" \
AM_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
AM_LISTEN=":${CORE_PORT}" AM_PUBLIC_URL="$URL" AM_RUNNER_URL="http://localhost:${RUNNER_PORT}" AM_STATIC_DIR="$WORK/web" \
AM_DEV=1 AM_MAILER=mailbox AM_MEDIA_STORE=fs AM_MEDIA_DIR="$WORK/media" \
  "$WORK/core" serve >"$WORK/core.log" 2>&1 &
CORE_PID=$!
stop() { kill "$CORE_PID" 2>/dev/null || true; wait "$CORE_PID" 2>/dev/null || true; }
trap 'stop; rm -rf "$WORK"' EXIT

for i in $(seq 1 40); do
  curl -sf "$URL/readyz" -o /dev/null && break
  kill -0 "$CORE_PID" 2>/dev/null || { cat "$WORK/core.log"; die "the core exited"; }
  sleep 0.5
done
curl -sf "$URL/readyz" -o /dev/null || { cat "$WORK/core.log"; die "the core did not become ready"; }
ok "core ready on $URL"

set +e
(cd "$APP" && URL="$URL" RUNNER_PORT="$RUNNER_PORT" OUTAGE_SECONDS="$OUTAGE_SECONDS" STAFF_EMAIL="$STAFF" bun e2e-core.mjs)
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  info "core log (last 40 lines):"
  tail -n 40 "$WORK/core.log"
fi
exit "$rc"
