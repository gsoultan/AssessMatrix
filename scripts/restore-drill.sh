#!/usr/bin/env bash
# Restore drill: prove a point-in-time restore works, end to end, on
# throwaway containers (docs/13 §3). Nothing here touches a real database.
#
#   1. a Postgres configured as in compose.yaml (WAL archived as it fills)
#   2. the real schema, by the real image's `migrate`
#   3. a base backup, then writes before and after a moment T
#   4. a fresh Postgres restored from the base backup and the WAL archive,
#      to T exactly
#   5. checks: the write before T is there, the one after is not, the schema
#      is whole, and the core starts on it and reports ready
#
# Needs docker and the image (docker compose build, or
# docker build -f deploy/Dockerfile -t assessmatrix:local .).
#   AM_IMAGE        default assessmatrix:local
#   POSTGRES_IMAGE  default postgres:17
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

AM_IMAGE="${AM_IMAGE:-assessmatrix:local}"
PG_IMAGE="${POSTGRES_IMAGE:-postgres:17}"
RUN="amdrill-$(date +%s)"
NET="$RUN-net"
PRIMARY="$RUN-primary"
RESTORED="$RUN-restored"
PW="drill-$(head -c 8 /dev/urandom | od -An -tx1 | tr -d ' \n')"

cleanup() {
  docker rm -f "$PRIMARY" "$RESTORED" "$RUN-core" >/dev/null 2>&1 || true
  docker volume rm -f "$RUN-data" "$RUN-archive" "$RUN-restore" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

psql_in() { docker exec -u postgres "$1" psql -X -qtA -v ON_ERROR_STOP=1 -d assessmatrix -c "$2"; }
wait_pg() {
  for _ in $(seq 1 60); do
    docker exec -u postgres "$1" pg_isready -q -d assessmatrix 2>/dev/null && return 0
    sleep 1
  done
  docker logs "$1" | tail -20
  die "$1 did not become ready"
}

docker network create "$NET" >/dev/null

bold "1 · primary, archiving WAL"
docker run -d --name "$PRIMARY" --network "$NET" \
  -e POSTGRES_PASSWORD="$PW" -e AM_OWNER_PASSWORD="$PW" -e AM_APP_PASSWORD="$PW" \
  -v "$RUN-data:/var/lib/postgresql/data" -v "$RUN-archive:/archive" \
  -v "$ROOT/deploy/postgres/init.sh:/docker-entrypoint-initdb.d/init.sh:ro" \
  "$PG_IMAGE" bash -c "mkdir -p /archive/wal && chown -R postgres /archive && exec docker-entrypoint.sh postgres \
    -c wal_level=replica -c archive_mode=on -c archive_timeout=60 \
    -c archive_command='test ! -f /archive/wal/%f && cp %p /archive/wal/%f'" >/dev/null
wait_pg "$PRIMARY"
ok "primary up"

bold "2 · schema"
docker run --rm --network "$NET" -e AM_MIGRATE_DATABASE_URL="postgres://am_owner:$PW@$PRIMARY:5432/assessmatrix" \
  "$AM_IMAGE" migrate >/dev/null
migrations=$(psql_in "$PRIMARY" "SELECT count(*) FROM schema_migrations")
ok "migrated: $migrations migrations"

bold "3 · base backup, then writes either side of T"
docker exec -u postgres "$PRIMARY" pg_basebackup -D /archive/base -Ft -z -X none -c fast
psql_in "$PRIMARY" "CREATE TABLE drill (what text, at timestamptz DEFAULT clock_timestamp()); INSERT INTO drill (what) VALUES ('before')"
sleep 1
T=$(psql_in "$PRIMARY" "SELECT clock_timestamp()")
sleep 1
psql_in "$PRIMARY" "INSERT INTO drill (what) VALUES ('after')"
# Close the segment so everything up to now reaches the archive.
wal=$(psql_in "$PRIMARY" "SELECT pg_walfile_name(pg_switch_wal())")
for _ in $(seq 1 30); do
  [ "$(psql_in "$PRIMARY" "SELECT coalesce(last_archived_wal, '') >= '$wal' FROM pg_stat_archiver")" = "t" ] && break
  sleep 1
done
docker stop "$PRIMARY" >/dev/null
ok "T = $T; primary stopped"

bold "4 · restore to T on a fresh server"
docker run -d --name "$RESTORED" --network "$NET" \
  -v "$RUN-restore:/var/lib/postgresql/data" -v "$RUN-archive:/archive:ro" \
  "$PG_IMAGE" bash -c "set -e
    D=/var/lib/postgresql/data/pgdata; mkdir -p \$D
    tar -xzf /archive/base/base.tar.gz -C \$D
    touch \$D/recovery.signal
    printf \"restore_command = 'cp /archive/wal/%%f %%p'\nrecovery_target_time = '$T'\nrecovery_target_action = 'promote'\n\" >> \$D/postgresql.auto.conf
    chown -R postgres \$D && chmod 700 \$D
    exec gosu postgres postgres -D \$D" >/dev/null
for _ in $(seq 1 60); do
  [ "$(docker exec -u postgres "$RESTORED" psql -X -qtA -d assessmatrix -c 'SELECT NOT pg_is_in_recovery()' 2>/dev/null)" = "t" ] && break
  sleep 1
done
[ "$(psql_in "$RESTORED" "SELECT NOT pg_is_in_recovery()")" = "t" ] || { docker logs "$RESTORED" | tail -20; die "restore did not finish"; }
ok "restored and promoted"

bold "5 · checks"
got=$(psql_in "$RESTORED" "SELECT string_agg(what, ',' ORDER BY at) FROM drill")
[ "$got" = "before" ] || die "expected only the write before T, got '$got'"
ok "the write before T is there, the one after is not"
[ "$(psql_in "$RESTORED" "SELECT count(*) FROM schema_migrations")" = "$migrations" ] || die "schema incomplete"
ok "schema whole"
docker run -d --name "$RUN-core" --network "$NET" \
  -e AM_DATABASE_URL="postgres://am_app:$PW@$RESTORED:5432/assessmatrix" \
  -e AM_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')" -e AM_PUBLIC_URL=http://drill.local -e AM_DEV=1 \
  "$AM_IMAGE" serve >/dev/null
for _ in $(seq 1 30); do
  docker exec "$RESTORED" bash -c "exec 3<>/dev/tcp/$RUN-core/8080 && printf 'GET /readyz HTTP/1.0\r\n\r\n' >&3 && cat <&3" 2>/dev/null | grep -q '^ready' && { ok "the core is ready on the restored database"; ok "restore drill passed"; exit 0; }
  sleep 1
done
docker logs "$RUN-core" | tail -20
die "the core did not become ready on the restored database"
