"""The psychometrics worker: computes item statistics outside any request.

Loop: take the oldest stale exam version (am_take_analysis, the one
cross-tenant step, returning ids only), scope the transaction to its tenant,
read its item scores under row-level security, write item_stats, commit. A
failure rolls back, which puts the exam version back in the queue.

Env: AM_DATABASE_URL (the application role). AM_WORKER_ONCE=1 drains the
queue once and exits (tests and cron).
"""

from __future__ import annotations

import json
import logging
import os
import signal
import sys
import time

import psycopg

import ctt

log = logging.getLogger("psychometrics")
IDLE_SECONDS = float(os.environ.get("AM_WORKER_IDLE", "5"))


def run_one(conn: psycopg.Connection, once: bool = False) -> bool:
    """Process one queued exam version. False when the queue is empty.

    A version whose analysis fails goes to the back of the queue rather than
    staying at its head, so one bad version never stalls every tenant's.
    """
    with conn.transaction():
        row = conn.execute("SELECT tenant_id, exam_id, exam_version FROM am_take_analysis()").fetchone()
        if row is None:
            return False
        tenant, exam, version = row
        conn.execute("SELECT set_config('app.tenant_id', %s, true)", (str(tenant),))
        try:
            with conn.transaction():
                analyse(conn, exam, version)
        except psycopg.OperationalError:
            raise
        except Exception:
            if once:
                raise
            log.exception("analysis of exam %s v%s failed; queued again behind the rest", exam, version)
            conn.execute(
                """INSERT INTO analysis_queue (tenant_id, exam_id, exam_version) VALUES (am_tenant(), %s, %s)
                   ON CONFLICT (tenant_id, exam_id, exam_version) DO UPDATE SET requested_at = now()""",
                (exam, version),
            )
        return True


def analyse(conn: psycopg.Connection, exam, version) -> None:
    """Compute and store one exam version's item statistics, inside the tenant's transaction."""
    snap = conn.execute(
        "SELECT snapshot FROM exam_versions WHERE exam_id = %s AND version = %s", (exam, version)
    ).fetchone()
    if snap is None:
        log.warning("exam %s v%s has no snapshot; skipped", exam, version)
        return
    items = ctt.items_of(snap[0] if isinstance(snap[0], dict) else json.loads(snap[0]))
    rows = conn.execute(
        """SELECT s.attempt_id::text, s.item_id, s.points, s.max_points, s.choices
           FROM item_scores s JOIN attempts a ON a.id = s.attempt_id
           WHERE s.exam_id = %s AND s.exam_version = %s AND a.submitted_at IS NOT NULL""",
        (exam, version),
    ).fetchall()
    scores = [ctt.Score(attempt=r[0], item=r[1], points=r[2], max_points=r[3], picked=list(r[4] or [])) for r in rows]
    stats = ctt.analyze(items, scores, version)
    stats["computedAt"] = int(time.time() * 1000)
    conn.execute(
        """INSERT INTO item_stats (tenant_id, exam_id, exam_version, stats) VALUES (am_tenant(), %s, %s, %s)
           ON CONFLICT (tenant_id, exam_id, exam_version) DO UPDATE SET stats = excluded.stats, computed_at = now()""",
        (exam, version, json.dumps(stats)),
    )
    log.info("analysed exam %s v%s over %d attempts", exam, version, stats["n"])


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    url = os.environ.get("AM_DATABASE_URL")
    if not url:
        log.error("AM_DATABASE_URL is required")
        return 2
    once = os.environ.get("AM_WORKER_ONCE") == "1"
    stop = False

    def halt(*_):
        nonlocal stop
        stop = True

    signal.signal(signal.SIGTERM, halt)
    signal.signal(signal.SIGINT, halt)
    while not stop:
        try:
            with psycopg.connect(url, autocommit=True) as conn:
                while not stop:
                    if not run_one(conn, once):
                        if once:
                            return 0
                        time.sleep(IDLE_SECONDS)
        except psycopg.OperationalError as e:
            if once:
                raise
            log.warning("database unavailable (%s); retrying", e)
            time.sleep(IDLE_SECONDS)
        except Exception:
            # The transaction rolled back, so the exam version is still queued.
            if once:
                raise
            log.exception("analysis failed; will retry")
            time.sleep(IDLE_SECONDS)
    return 0


if __name__ == "__main__":
    sys.exit(main())
