#!/usr/bin/env bash
# Start the AssessMatrix UI on a known port. Installs deps on first run.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — dev"
ensure_deps
free_port
info "starting Vite on port ${PORT}…"
cd "$APP"
PORT="${PORT}" exec bun run dev
