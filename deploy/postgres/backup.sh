#!/usr/bin/env bash
# A base backup, then pruning, inside the postgres container (docs/13 §3):
#
#   docker compose exec -u postgres postgres /backup.sh     # or: make backup
#
# 1. a compressed base backup into /archive/base-<UTC time>, written under a
#    .part name and renamed when whole, with the first WAL file it needs
# 2. only the newest AM_BACKUP_KEEP (default 7) base backups are kept
# 3. archived WAL older than the oldest kept backup is deleted: nothing can
#    restore from it any more, and without this the archive only ever grows
#
# Run it nightly. A restore needs one base backup and the WAL after it, so
# with daily backups the machine holds about AM_BACKUP_KEEP days. Copy
# /archive off the machine too: a backup on the same disk is not a backup.
set -euo pipefail

ARCHIVE="${ARCHIVE:-/archive}"
KEEP="${AM_BACKUP_KEEP:-7}"
[ "$KEEP" -ge 1 ] || { echo "AM_BACKUP_KEEP must be at least 1" >&2; exit 1; }

dir="$ARCHIVE/base-$(date -u +%Y%m%dT%H%M%SZ)"
rm -rf "$ARCHIVE"/base-*.part
pg_basebackup -D "$dir.part" -Ft -z -X none -c fast
lsn=$(sed -n 's/.*"Start-LSN": *"\([^"]*\)".*/\1/p' "$dir.part/backup_manifest" | head -1)
[ -n "$lsn" ] || { echo "no Start-LSN in the backup manifest" >&2; exit 1; }
psql -XqtA -d postgres -c "SELECT pg_walfile_name('$lsn')" > "$dir.part/START_WAL"
mv "$dir.part" "$dir"
echo "base backup: $dir (from $(cat "$dir/START_WAL"))"

# Oldest first; the names sort by time.
mapfile -t all < <(find "$ARCHIVE" -maxdepth 1 -type d -name 'base-*' ! -name '*.part' | sort)
for old in "${all[@]:0:$(( ${#all[@]} > KEEP ? ${#all[@]} - KEEP : 0 ))}"; do
  rm -rf "$old"
  echo "removed $old"
done
mapfile -t kept < <(find "$ARCHIVE" -maxdepth 1 -type d -name 'base-*' ! -name '*.part' | sort)
oldest="${kept[0]}"
if [ -f "$oldest/START_WAL" ]; then
  # -x .gz: the archive holds segments gzipped (compose.yaml archive_command).
  pg_archivecleanup -x .gz "$ARCHIVE/wal" "$(cat "$oldest/START_WAL")"
  # Segments archived before compression was turned on.
  pg_archivecleanup "$ARCHIVE/wal" "$(cat "$oldest/START_WAL")"
  # And the small history file each base backup leaves, once it is older.
  for f in "$ARCHIVE"/wal/*.backup.gz; do
    [ -e "$f" ] && [[ "$(basename "$f")" < "$(cat "$oldest/START_WAL")" ]] && rm -f "$f"
  done
  echo "WAL before $(cat "$oldest/START_WAL") removed"
else
  echo "the oldest base backup ($oldest) predates this script; WAL kept until it ages out" >&2
fi
