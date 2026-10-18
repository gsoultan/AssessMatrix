#!/usr/bin/env bash
# First run on a fresh install, in a browser: the core starts on an empty
# database, sets up its schema itself, and /setup creates the organisation and
# its administrator once.
#
#   AM_E2E_SETUP_MIGRATE_DATABASE_URL  the schema owner of a database this test may wipe
#   AM_E2E_SETUP_DATABASE_URL          the unprivileged application role, same database
#
# The database is emptied first (its public schema dropped and remade), so
# point these at a throwaway database, never a real one.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — first-run setup on a fresh install"
ensure_deps
: "${AM_E2E_SETUP_MIGRATE_DATABASE_URL:?set AM_E2E_SETUP_MIGRATE_DATABASE_URL to the owner role of a throwaway database}"
: "${AM_E2E_SETUP_DATABASE_URL:?set AM_E2E_SETUP_DATABASE_URL to the app role of the same database}"
CORE_PORT="${CORE_PORT:-8093}"
METRICS_PORT="${METRICS_PORT:-8094}"
URL="http://localhost:${CORE_PORT}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

info "emptying the throwaway database"
PGOPTIONS='-c client_min_messages=warning' psql -q -v ON_ERROR_STOP=1 "$AM_E2E_SETUP_MIGRATE_DATABASE_URL" \
  -c 'DROP SCHEMA public CASCADE' -c 'CREATE SCHEMA public' >/dev/null

info "building the studio for the core"
(cd "$APP" && VITE_BACKEND=core bunx vite build --outDir "$WORK/web" --emptyOutDir >/dev/null)

info "building the core"
(cd "$ROOT/services/core" && go build -o "$WORK/core" ./cmd/core)

lsof -ti tcp:"$CORE_PORT" 2>/dev/null | xargs kill -9 2>/dev/null || true
# The owner URL too: on a single machine, serve sets up the schema itself.
AM_MIGRATE_DATABASE_URL="$AM_E2E_SETUP_MIGRATE_DATABASE_URL" AM_DATABASE_URL="$AM_E2E_SETUP_DATABASE_URL" \
AM_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
AM_LISTEN=":${CORE_PORT}" AM_METRICS_LISTEN=":${METRICS_PORT}" AM_PUBLIC_URL="$URL" AM_STATIC_DIR="$WORK/web" \
AM_DEV=1 AM_MAILER=log \
  "$WORK/core" serve >"$WORK/core.log" 2>&1 &
CORE_PID=$!
trap 'kill "$CORE_PID" 2>/dev/null || true; wait "$CORE_PID" 2>/dev/null || true; rm -rf "$WORK"' EXIT

for i in $(seq 1 40); do
  curl -sf "$URL/readyz" -o /dev/null && break
  kill -0 "$CORE_PID" 2>/dev/null || { cat "$WORK/core.log"; die "the core exited"; }
  sleep 0.5
done
curl -sf "$URL/readyz" -o /dev/null || { cat "$WORK/core.log"; die "the core did not become ready"; }
ok "core ready on $URL, schema set up at start"

CODE="$(sed -n 's/.*"setup_code":"\([A-Z0-9-]*\)".*/\1/p' "$WORK/core.log" | head -1)"
[ -n "$CODE" ] || { cat "$WORK/core.log"; die "the core did not print a setup code"; }
ok "the core printed where to finish setup, and its code"

set +e
(cd "$APP" && URL="$URL" SETUP_CODE="$CODE" bun e2e-setup.mjs)
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  info "core log (last 30 lines):"
  tail -n 30 "$WORK/core.log"
fi
exit "$rc"
