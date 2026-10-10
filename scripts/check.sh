#!/usr/bin/env bash
# Type-check only. Fast — this is the one to run in a watch loop.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — typecheck"
ensure_deps
cd "$APP"
bunx tsc --noEmit
ok "types clean"
