# 13 · Operations: deploy, observe, back up, restore

How AssessMatrix runs in production. Written for the deploy, observability and data-safety
tracks of docs/09; the privacy rules it relies on are in docs/10 and the security controls in
docs/12.

## 1. What runs

| Piece | Image | Notes |
| :--- | :--- | :--- |
| core | `assessmatrix` (`deploy/Dockerfile`) | Go binary plus the three web apps it serves: studio at `/`, runner at `/take/`, credential check at `/verify/`. Distroless, non-root. Commands: `migrate`, `serve`, `tenant`, `super-admin`, `setup-code` |
| psychometrics | `assessmatrix-psychometrics` | Python worker; item statistics only |
| Postgres 17 | managed, or `postgres:17` in compose | All state. In the tenants' region (Indonesia for `id`) |
| Object storage | S3-compatible bucket in the same region | Images; required with more than one core replica |
| Mail | any SMTP relay | Invitations, codes, credential links |

The core listens on 8080 (app) and 9090 (Prometheus metrics, never exposed publicly). It stops
taking requests on SIGTERM and drains for up to 20 s.

## 2. Two ways to deploy

**One machine** (pilot, small self-host): `compose.yaml`. Fill `.env` from `.env.example`,
then `docker compose --profile tls up -d`. Caddy (`deploy/caddy/Caddyfile`) gets TLS
certificates for `AM_DOMAIN` (and `AM_RUNNER_DOMAIN`) by itself. Postgres archives WAL to its
own volume; see §3 for getting it off the machine.

**First run.** Open `https://AM_DOMAIN/setup`. The core prints the setup code in its log
(`docker compose logs core | grep setup_code`, or `docker compose exec core /core setup-code`).
The page shows the database is connected and migrated, then creates the super administrator
and the default tenant (which they also administer) and signs them in. It works once; afterwards
it says the server is set up. The super administrator creates further tenants and chooses each
one's administrators from Access; administrators add users to their own tenants as members.
`core super-admin <email>` makes another existing user a super administrator, or recovers the
role if nobody holding it can sign in.

**Kubernetes** (`deploy/k8s`, kustomize): 3 core replicas across zones with a disruption budget,
the restricted pod security profile, read-only root filesystems, network policies, and an
ingress with streaming-friendly timeouts. Secrets come from the cluster's secret store, never
from git (`secret.example.yaml` shows the keys). Postgres should be a managed service with
point-in-time recovery in the same region.

### Releasing

1. Tag `vX.Y.Z` on `main`. `.github/workflows/release.yml` builds both images, pushes them to
   GHCR with an SBOM and signed build provenance. Nothing else publishes.
2. `kustomize edit set image assessmatrix=ghcr.io/…/assessmatrix:X.Y.Z` (and psychometrics).
3. Apply the `migrate` Job; wait for it to complete. Migrations are forward-only and safe to run
   twice (advisory lock).
4. Roll out the Deployments. The core's readiness probe fails until the database answers, and
   the rollout keeps every old pod until a new one is ready.
5. Never during a live exam window: check the schedule first (docs/11).

Rollback: deploy the previous tag. Schema changes are additive, so the previous core runs on
the newer schema; a migration that is not additive needs its own plan, written before it ships.

## 3. Backups and restore

**What must survive:** the database (everything, including the hash chains and the sealed
issuer keys) and `AM_SECRET`. Without `AM_SECRET` the issuer keys cannot be opened and no
attempt log verifies: keep it in two separate secret stores.

**Targets:** lose at most 1 minute of committed work (RPO), be back within 1 hour (RTO).

- **Managed Postgres:** turn on point-in-time recovery with at least 7 days, daily snapshots
  kept 35 days, in the same region.
- **Compose:** WAL is archived each minute, gzipped, to the `walarchive` volume. Run
  `make backup` nightly (cron: `0 2 * * * cd /srv/assessmatrix && make backup`). It takes a
  base backup into `/archive/base-<time>`, keeps the newest `AM_BACKUP_KEEP` (default 7), and
  deletes archived WAL that no kept backup needs, so the volume stays at about a week of
  history instead of growing until the disk is full. Copy the volume to object storage in the
  same region, encrypted, with a 35-day lifecycle rule. To restore, unpack a
  `base-*/base.tar.gz` and use `restore_command = 'gunzip -c /archive/wal/%f.gz > %p'`, as
  `scripts/restore-drill.sh` does.
- **Images:** the media bucket replicates to a second bucket in the same region. Turn bucket
  versioning off, or expire old versions after 35 days: a deleted image must actually go (§5).

**Restore drill:** `scripts/restore-drill.sh` proves the procedure end to end on throwaway
containers: base backup, writes either side of a moment T, a fresh server restored to T, the
write before T present and the one after absent, and the core ready on the result. It runs in
CI on every PR. Once a quarter, also restore a real backup into a scratch environment, run
`GetAttemptLog` on a sample of attempts to confirm their chains verify, and record the time it
took here.

| Date | Backup restored | Time to ready | Chains verified | By |
| :--- | :--- | :--- | :--- | :--- |
| | | | | |

## 4. Observing

Scrape the core's port 9090 as job `assessmatrix-core`. Series (labels are procedure and code
only, no personal data):

- `am_rpc_requests_total{procedure,code}`, `am_rpc_duration_seconds{procedure}`: rate, errors,
  duration per procedure.
- `am_events_stored_total`: candidates' events written. Flat during a live exam means trouble.
- `am_sync_lag_seconds`: from a candidate's device to the server; the long tail is offline time.
- `am_signals_dropped_total`: integrity signals refused for want of consent.
- `am_records_purged_total{why}`: retention and erasure at work.

`deploy/prometheus/alerts.yml` pages when the core is down or more than 1% of answer saves or
hand-ins fail, and opens tickets for slow calls, late answers and signals without consent;
`alerts_test.yml` tests the paging rules. Logs are JSON lines on stdout, one per request,
never with bodies.

## 5. Retention and erasure

An hourly job deletes each attempt's record (answers, integrity signals, per-item index,
uploaded images) once its tenant's period has passed: 365 days by default, never longer (a
database check; the consent text promises a year). The attempt keeps its score and the date its
record went. Sent mail is deleted after 30 days. Staff erase a person on request from the
studio (People, Erase); that removes the person, their attempts, credentials, images and mail.
Both write an audit record by id.

Backups still hold deleted data until they expire (35 days, §3). The privacy notice says so,
and a restore re-runs retention within the hour; an erasure made after the backup was taken
must be re-applied by hand after a restore (keep the audit log's `person.erased` ids: they say
which).

## 6. Known limits

- Rate limits on sign-in and link requests are per core instance (docs/12 §4).
- Item statistics already computed keep counting attempts whose records were purged later; a
  recomputation after a purge counts only what is left.

## 7. Load

`make load` (or `N=… MINUTES=… RAMP=… scripts/load.sh`) runs `services/core/cmd/loadsim`
against a local core: the whole cohort starts within `RAMP`, autosaves one or two answers every
5–15 s, retries a failed save with the same batch id as the runner does, and hands in. It then
reads every attempt's log and counts acknowledged answers that are missing; anything above zero
fails the run.

| Date | Where | Cohort | Calls | AppendEvents p95 / p99 | Errors | Answers lost |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 2026-10-10 | local, one core, one Postgres | 500 in 1 min, 3 min each | 12,704 | 8 ms / 12 ms | 0 | 0 |

Before the pilot, run it against staging at the partner's cohort size with 2× headroom
(the core must run with `AM_DEV=1 AM_MAILER=mailbox AM_TRUST_PROXY=1` for the run; reset
those afterwards).

## 8. Incident runbook (live exam)

Have this open, with the proctor monitor, for every live exam window (docs/11 §3).

**Who is paged.** `deploy/prometheus/alerts.yml` pages on `CoreDown`, `AnswersNotSaving` and
`ServerErrors`; everything else opens a ticket. Name both people before each window and write
them here; the first acknowledges within 5 minutes, the second if not.

| Window | On call (pages) | Second | Partner's contact |
| :--- | :--- | :--- | :--- |
| | | | |

**First rule.** A candidate's device keeps every answer (OPFS, sealed batches) and sends it
when it can; answers made before the deadline count for up to 30 minutes after it
(`domain.OfflineGrace`). So an outage loses no work by itself. What it loses is **time**, and
that is given back afterwards, never by restarting anything. Do not restart Postgres, roll back
or deploy during a window unless the core cannot come back otherwise.

**The core or the database is down.**

1. Note the time it went down. Check `docker compose ps` / `kubectl get pods`, then the
   core's log (`level":"ERROR"`) and `/readyz` (it checks Postgres).
2. Bring it back: restart the core; if Postgres is gone, see §3 (point-in-time restore to just
   before the failure). Tell the hall: "keep working, your answers are saved on this device".
3. Once `/readyz` answers, watch `am_events_stored_total` climb as devices send what they
   queued, and the monitor's sittings come back from "last heard from N min ago".
4. Note the time it came back. **Give the time back:** Proctor monitor, choose the exam,
   *Add time for everyone in progress*, the outage's minutes rounded up, with the reason in
   words for the candidate ("Server outage from 10:20 to 10:45"). Each deadline moves by
   those minutes; each candidate sees the reason in their record. A device learns the new
   deadline on its next save, or when its own clock runs out.

**"Pausing" an exam.** There is no pause button, on purpose: a paused clock on thousands of
devices is one more thing that can disagree with the server. To pause (fire alarm, power cut in
the hall), tell candidates to stop, and when they resume, add the minutes they stopped for, as
above. Sittings already handed in are not reopened.

**One candidate's device died, or their browser will not reopen.** Assignments, their row,
*Resend*. They get a new link and code on any device; it opens the same attempt with every
answer the server already holds, and their old device sends anything it still holds if it
comes back. If time was lost, open their sitting in the monitor and *Add time* for them
alone, with the reason.

**A candidate says an answer is missing.** Open their sitting in the monitor: the attempt
record lists every event the server holds, in order, with the server's time, and says whether
the chain is intact. An answer that never reached the server is still on their device: do not
clear the browser, reopen the sitting there and let it send. Their own copy of the same
record is under *Apa yang kami catat* once they hand in.

**A sitting ran out of time while its device was offline.** The device cannot hand in until it
reaches the server, so the sitting stays in progress. When the device is back it sends its
answers and hands in; answers made before the deadline count. If the device is never coming
back, resend the link (above) and let the candidate hand in from another device; if the 30
minutes of offline grace have passed and the candidate lost time to the outage, add it first.

**The record says "altered".** A serious `record_altered` flag naming an event means a row was
changed outside the core. Do not score or issue anything from that attempt; preserve the
database (snapshot) and the core logs, and treat it as a security incident (docs/12).

**After the window.** Write down what happened, when, which sittings were given time and how
much (the audit log's `attempt.time_added` rows have the list), and anything the runbook did
not cover. That goes to the partner with the results (docs/11 §4).
