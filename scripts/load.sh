#!/usr/bin/env bash
# Cohort load test against a local core (docs/09, load track). Starts the core
# on the e2e database with a mailbox big enough for the cohort, runs
# services/core/cmd/loadsim, and fails if any acknowledged answer is missing.
#
#   N=200 MINUTES=3 scripts/load.sh        (RAMP=1m: everyone starts within it)
#
# Same environment as e2e-core: AM_E2E_MIGRATE_DATABASE_URL, AM_E2E_DATABASE_URL.
# For a staging run, point loadsim at the staging core directly (it must run
# with AM_DEV=1 AM_MAILER=mailbox AM_TRUST_PROXY=1 for the duration).
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"
: "${AM_E2E_MIGRATE_DATABASE_URL:?set AM_E2E_MIGRATE_DATABASE_URL to the owner role}"
: "${AM_E2E_DATABASE_URL:?set AM_E2E_DATABASE_URL to the app role}"
N="${N:-200}"
MINUTES="${MINUTES:-3}"
RAMP="${RAMP:-1m}"
PORT="${CORE_PORT:-8093}"
WORK="$(mktemp -d)"

info "building"
(cd "$ROOT/services/core" && go build -o "$WORK/core" ./cmd/core && go build -o "$WORK/loadsim" ./cmd/loadsim)
AM_MIGRATE_DATABASE_URL="$AM_E2E_MIGRATE_DATABASE_URL" "$WORK/core" migrate >/dev/null
STAFF="load.$(date +%s)@example.id"
AM_MIGRATE_DATABASE_URL="$AM_E2E_MIGRATE_DATABASE_URL" "$WORK/core" tenant "Load $(date +%s)" "$STAFF" >/dev/null

AM_DATABASE_URL="$AM_E2E_DATABASE_URL" AM_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
AM_LISTEN=":$PORT" AM_METRICS_LISTEN=":$((PORT + 1))" AM_PUBLIC_URL="http://localhost:$PORT" \
AM_DEV=1 AM_MAILER=mailbox AM_DEV_MAILBOX_KEEP=$((N * 4 + 100)) AM_TRUST_PROXY=1 \
  "$WORK/core" serve >"$WORK/core.log" 2>&1 &
CORE=$!
trap 'kill $CORE 2>/dev/null; wait $CORE 2>/dev/null; rm -rf "$WORK"' EXIT
for _ in $(seq 1 50); do curl -sf "http://localhost:$PORT/readyz" -o /dev/null && break; sleep 0.2; done

set +e
"$WORK/loadsim" -url "http://localhost:$PORT" -staff "$STAFF" -n "$N" -minutes "$MINUTES" -ramp "$RAMP"
rc=$?
set -e
info "server side:"
curl -s "http://localhost:$((PORT + 1))/metrics" | grep -E '^am_(events_stored_total|rpc_requests_total\{code="(internal|unknown|unavailable)")' || true
[ "$rc" -eq 0 ] || { tail -20 "$WORK/core.log"; }
exit "$rc"
