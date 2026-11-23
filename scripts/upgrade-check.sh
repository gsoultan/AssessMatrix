#!/usr/bin/env bash
# Backward compatibility of an update, end to end (docs/13 §2):
#
#   1. the previous release's core sets up a database and a tenant in it
#   2. this commit's core migrates that database          (the update)
#   3. the previous core serves on the migrated schema     (a rollback, and the
#                                                           minutes of an update
#                                                           before the restart)
#   4. this commit's core serves on it                     (after the update)
#   5. the previous release's API tests pass on the migrated schema, so every
#      endpoint an older core serves still works on it, not only its start
#
# Each core must become ready and answer an RPC. The previous release is
# AM_UPGRADE_FROM (a tag or commit), by default the newest v* tag, or the
# merge base with origin/main when nothing is tagged yet.
#
#   AM_E2E_SETUP_MIGRATE_DATABASE_URL  the schema owner of a database this test may wipe
#   AM_E2E_SETUP_DATABASE_URL          the unprivileged application role, same database
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — upgrade from the previous release, and roll back"
: "${AM_E2E_SETUP_MIGRATE_DATABASE_URL:?set AM_E2E_SETUP_MIGRATE_DATABASE_URL to the owner role of a throwaway database}"
: "${AM_E2E_SETUP_DATABASE_URL:?set AM_E2E_SETUP_DATABASE_URL to the app role of the same database}"
PORT_OLD="${PORT_OLD:-8095}"
PORT_NEW="${PORT_NEW:-8096}"

cd "$ROOT"
FROM="${AM_UPGRADE_FROM:-}"
if [ -z "$FROM" ]; then
  FROM="$(git describe --tags --abbrev=0 --match 'v*.*.*' 2>/dev/null || true)"
fi
if [ -z "$FROM" ]; then
  FROM="$(git merge-base HEAD origin/main 2>/dev/null || true)"
  [ "$FROM" != "$(git rev-parse HEAD)" ] || FROM="HEAD~1"
fi
info "previous release: $FROM ($(git rev-parse --short "$FROM"))"

WORK="$(mktemp -d)"
cleanup() {
  [ -z "${OLD_PID:-}" ] || kill "$OLD_PID" 2>/dev/null || true
  [ -z "${NEW_PID:-}" ] || kill "$NEW_PID" 2>/dev/null || true
  git worktree remove --force "$WORK/old" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

info "emptying the throwaway database"
PGOPTIONS='-c client_min_messages=warning' psql -q -v ON_ERROR_STOP=1 "$AM_E2E_SETUP_MIGRATE_DATABASE_URL" \
  -c 'DROP SCHEMA public CASCADE' -c 'CREATE SCHEMA public' >/dev/null

info "building both cores"
git worktree add --detach "$WORK/old" "$FROM" >/dev/null 2>&1
(cd "$WORK/old/services/core" && go build -o "$WORK/core-old" ./cmd/core)
(cd "$ROOT/services/core" && go build -o "$WORK/core-new" ./cmd/core)

SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
owner() { AM_MIGRATE_DATABASE_URL="$AM_E2E_SETUP_MIGRATE_DATABASE_URL" AM_SECRET="$SECRET" "$@"; }

owner "$WORK/core-old" migrate >/dev/null
owner "$WORK/core-old" tenant "Before the update" "upgrade.$$@example.id" >/dev/null
ok "the previous release set up the database"

owner "$WORK/core-new" migrate >"$WORK/migrate.log" 2>&1 || { cat "$WORK/migrate.log" >&2; die "this commit could not migrate the previous release's database"; }
ok "this commit migrated it"

serve() { # BINARY PORT LOG
  AM_DATABASE_URL="$AM_E2E_SETUP_DATABASE_URL" AM_SECRET="$SECRET" AM_LISTEN=":$2" AM_METRICS_LISTEN=off \
  AM_PUBLIC_URL="http://localhost:$2" AM_DEV=1 AM_MAILER=log AM_UPDATE_CHECK=off \
    "$1" serve >"$3" 2>&1 &
}
answers() { # PORT LOG NAME: ready, and an RPC that reads the database
  local i
  for i in $(seq 1 60); do
    if curl -fsS "http://localhost:$1/readyz" >/dev/null 2>&1; then
      curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' \
        "http://localhost:$1/assessmatrix.v1.SetupService/GetSetupStatus" >/dev/null ||
        { cat "$2" >&2; die "$3 is ready but its RPCs fail"; }
      return 0
    fi
    sleep 0.5
  done
  cat "$2" >&2
  die "$3 did not become ready on the migrated database"
}

serve "$WORK/core-old" "$PORT_OLD" "$WORK/old.log"; OLD_PID=$!
answers "$PORT_OLD" "$WORK/old.log" "the previous release"
ok "the previous release serves on the new schema (a rollback works)"

serve "$WORK/core-new" "$PORT_NEW" "$WORK/new.log"; NEW_PID=$!
answers "$PORT_NEW" "$WORK/new.log" "this commit"
ok "this commit serves on it, beside the previous release (a rolling update works)"

info "the previous release's API tests, on the migrated schema"
# Skipped: tests that list the schema's tables and functions (a newer
# schema has more, by design) and first-run setup (its own fresh database).
(cd "$WORK/old/services/core" && CI= AM_TEST_DATABASE_URL="$AM_E2E_SETUP_DATABASE_URL" \
  AM_TEST_MIGRATE_DATABASE_URL="$AM_E2E_SETUP_MIGRATE_DATABASE_URL" \
  go test -count=1 -skip 'TestSecurityDefinerFunctionsAreReviewed|TestEveryTableIsTenantScoped|TestFirstRunSetupHappensOnce' \
  ./internal/api/ >"$WORK/oldtests.log" 2>&1) || { tail -40 "$WORK/oldtests.log" >&2; die "the previous release breaks on the migrated schema: a migration is not additive"; }
ok "every endpoint of the previous release still works on the new schema"
