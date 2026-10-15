#!/usr/bin/env bash
# Stop the development Postgres that `make up` started. Data stays in its
# volume; RESET=1 deletes it too, so the next `make up` starts empty.
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

bold "AssessMatrix — down"
command -v docker >/dev/null 2>&1 || die "docker is not installed"
cd "$ROOT"
if [ "${RESET:-}" = "1" ]; then
  docker compose down -v --remove-orphans
  ok "stopped, and the database volume removed"
else
  docker compose stop postgres
  ok "Postgres stopped; data kept (RESET=1 make down to wipe it)"
fi
