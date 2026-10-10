#!/usr/bin/env bash
# Remove build output and installed dependencies.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — clean"
rm -rf "$APP/dist" "$APP/node_modules" "$ROOT/node_modules"
ok "removed apps/studio/dist and node_modules"
info "exam data lives in browser localStorage — clear it from the app's 'Your exams' panel"
