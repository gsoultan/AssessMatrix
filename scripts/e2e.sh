#!/usr/bin/env bash
# Smoke-test the create-exam → take-exam → score loop in a real browser.
# Starts a dev server if one is not already running, and stops it again.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — e2e"
ensure_deps

if ! (cd "$APP" && bunx playwright --version >/dev/null 2>&1); then
  die "playwright is not installed. Run: bun install && bunx playwright install chromium (or set CHROMIUM_PATH)"
fi

started=""
if ! curl -sf "http://localhost:${PORT}" -o /dev/null 2>/dev/null; then
  info "no server on ${PORT} — starting one"
  (cd "$APP" && PORT="${PORT}" bun run dev >/tmp/assessmatrix-e2e.log 2>&1 &)
  started=1
  wait_for_server
else
  info "using the server already on ${PORT}"
fi

set +e
# Bun runs the test directly — no Node in the toolchain.
(cd "$APP" && URL="http://localhost:${PORT}" bun e2e.mjs)
rc=$?
set -e

if [ -n "$started" ]; then
  info "stopping the server we started"
  lsof -ti tcp:"${PORT}" 2>/dev/null | xargs kill -9 2>/dev/null || true
fi

exit "$rc"
