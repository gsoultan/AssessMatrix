#!/usr/bin/env bash
# Build, then serve the production bundle — the honest check before shipping.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — preview"
"$ROOT/scripts/build.sh"
free_port
cd "$APP"
PORT="${PORT}" exec bun run preview
