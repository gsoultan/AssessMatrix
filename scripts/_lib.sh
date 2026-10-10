# shellcheck shell=bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="$ROOT/apps/studio"
PORT="${PORT:-5180}"

bold()  { printf '\033[1m%s\033[0m\n' "$*"; }
info()  { printf '\033[2m›\033[0m %s\n' "$*"; }
ok()    { printf '\033[32m✓\033[0m %s\n' "$*"; }
warn()  { printf '\033[33m!\033[0m %s\n' "$*"; }
die()   { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

need_bun() {
  command -v bun >/dev/null 2>&1 || die "bun is not installed. Get it with: curl -fsSL https://bun.sh/install | bash"
}

ensure_deps() {
  need_bun
  # Bun workspaces: one install at the root serves every app and package.
  if [ ! -d "$ROOT/node_modules" ]; then
    info "installing dependencies…"
    (cd "$ROOT" && bun install)
  fi
}

# The dev server silently hops to the next free port when 5180 is taken, which
# makes screenshots and bookmarks point at a stale server. Clear it instead.
free_port() {
  local pids
  pids="$(lsof -ti tcp:"${PORT}" 2>/dev/null | grep -v "^$$\$" || true)"
  if [ -n "$pids" ]; then
    warn "port ${PORT} is in use (pid $(echo "$pids" | tr '\n' ' ')) — freeing it"
    echo "$pids" | xargs kill -9 2>/dev/null || true
    sleep 1
  fi
}

wait_for_server() {
  local i
  for i in $(seq 1 40); do
    if curl -sf "http://localhost:${PORT}" -o /dev/null 2>/dev/null; then
      ok "ready on http://localhost:${PORT} (${i}s)"
      return 0
    fi
    sleep 1
  done
  die "server did not come up on port ${PORT}"
}
