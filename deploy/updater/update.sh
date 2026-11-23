#!/usr/bin/env bash
# The updater for a compose install (docs/13 §2, "Updates"). Run it every
# minute (assessmatrix-update.timer, or cron) from the directory holding
# compose.yaml and .env:
#
#   deploy/updater/update.sh            # or: make update
#
# Each run tells the core it is alive and asks whether a super administrator
# has asked for an update (Updates screen). If one is waiting, and no sitting
# is in progress (unless they chose "now"), it:
#
#   1. backs the database up                   (nothing changed yet if it fails)
#   2. pulls the release's images, by digest   (nothing changed yet if it fails)
#   3. points .env at them and migrates        (migrations only add, so the
#                                               running core is unaffected)
#   4. restarts the core and waits until it is ready
#   5. if it is not ready in time, puts the previous images back and
#      restarts again: the previous core runs on the newer schema
#
# and reports each step back, so the studio shows what happened. The core
# itself never pulls or restarts anything, and nothing it serves can start
# this: a request only records which release; the image comes from the
# release's manifest, pinned by digest, and must be in AM_UPDATE_IMAGE_REPO.
set -euo pipefail

cd "${AM_INSTALL_DIR:-$(dirname "$0")/../..}"
[ -f compose.yaml ] && [ -f .env ] || { echo "run from the directory with compose.yaml and .env" >&2; exit 1; }

# One run at a time: a slow update outlives the timer's minute.
exec 9>.update.lock
flock -n 9 || exit 0

REPO="${AM_UPDATE_IMAGE_REPO:-ghcr.io/gsoultan/assessmatrix}"
READY_TIMEOUT="${AM_UPDATE_READY_TIMEOUT:-180}"
# The `core update` output this script understands (cmd/core/updater.go).
PROTOCOL=1

dc() { docker compose "$@"; }
log() { echo "$(date -u +%FT%TZ) $*"; }
# A field of the one-line JSON `core update claim` prints. Its values are
# versions, ids and image references: no quotes or escapes inside.
field() { printf '%s' "$claim" | sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\)\"\{0,1\}[,}].*/\1/p"; }

claim=$(dc run --rm -T --no-deps migrate update claim "$(hostname)" | tail -n 1)
proto=$(field protocol)
if [ "$proto" != "$PROTOCOL" ]; then
  log "this updater speaks protocol $PROTOCOL; the core printed: $claim. Update this checkout (git pull)." >&2
  exit 1
fi
id=$(field id)
[ -n "$id" ] || exit 0

from=$(field from_version)
target=$(field target_version)
image=$(field image)
psy=$(field psychometrics_image)

report() {
  log "$target: $1: $2"
  dc run --rm -T --no-deps migrate update report "$id" "$1" "$2" >/dev/null || log "could not record that step" >&2
}
fail() { report failed "$1"; exit 1; }

digest='@sha256:[0-9a-f]\{64\}$'
printf '%s' "$image" | grep -q "^$REPO$digest" ||
  fail "The release's image $image is not from $REPO, pinned by digest; nothing was changed"
if [ -n "$psy" ]; then
  printf '%s' "$psy" | grep -q "^$REPO-psychometrics$digest" ||
    fail "The release's psychometrics image $psy is not from $REPO-psychometrics; nothing was changed"
fi

set_env() { # KEY VALUE: replace the line, or add it
  if grep -q "^$1=" .env; then
    sed -i.tmp "s|^$1=.*|$1=$2|" .env && rm -f .env.tmp
  else
    printf '%s=%s\n' "$1" "$2" >> .env
  fi
}
restart() { dc up -d --no-deps --no-build core psychometrics; }
ready() { # wait until the core answers ready, and says which version it is
  local deadline=$((SECONDS + READY_TIMEOUT)) got
  while [ $SECONDS -lt $deadline ]; do
    if got=$(dc exec -T core /core probe 2>/dev/null) && [ "$got" = "$1" ]; then return 0; fi
    sleep 3
  done
  return 1
}

report running "Backing up the database"
dc exec -T -u postgres postgres /backup.sh ||
  fail "The database backup failed, so nothing was changed. Check the postgres container's disk and logs"

report running "Downloading $target"
docker pull -q "$image" >/dev/null || fail "Downloading $target failed, so nothing was changed"
if [ -n "$psy" ]; then
  docker pull -q "$psy" >/dev/null || fail "Downloading $target's psychometrics worker failed, so nothing was changed"
fi
if [ "${AM_UPDATE_VERIFY:-0}" = 1 ]; then
  # Signed build provenance from the release workflow (needs the gh CLI).
  gh attestation verify "oci://$image" --repo "${REPO#ghcr.io/}" >/dev/null ||
    fail "$target's image did not verify against its build provenance; nothing was changed"
fi

cp .env .env.before-update
set_env AM_IMAGE "$image"
[ -z "$psy" ] || set_env AM_PSYCHOMETRICS_IMAGE "$psy"

report running "Migrating the database"
if ! dc run --rm -T --no-deps migrate; then
  cp .env.before-update .env
  fail "Migrating the database for $target failed; the server stays on $from, which runs on what was applied"
fi

report running "Restarting on $target"
restart
if ready "$target"; then
  report succeeded "Now on $target. The previous version's images are kept for a manual rollback (.env.before-update)"
  exit 0
fi

log "$target did not become ready in ${READY_TIMEOUT}s; going back to $from" >&2
dc logs --tail 50 core >&2 || true
cp .env.before-update .env
restart
if ready "$from"; then
  report rolled_back "$target did not become ready within ${READY_TIMEOUT} seconds, so the server went back to $from. The database keeps $target's additions, which $from ignores"
else
  report failed "$target did not become ready, and neither did $from after going back. Check \`docker compose logs core\` now"
fi
exit 1
