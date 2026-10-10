#!/usr/bin/env bash
# Type-check then produce a production build in apps/studio/dist.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — build"
ensure_deps
cd "$APP"
bun run build
ok "built → apps/studio/dist"
