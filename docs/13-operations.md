# 13 · Operations: deploy, observe, back up, restore

How AssessMatrix runs in production. Written for the deploy, observability and data-safety
tracks of docs/09; the privacy rules it relies on are in docs/10 and the security controls in
docs/12.

## 1. What runs

| Piece | Image | Notes |
| :--- | :--- | :--- |
| core | `assessmatrix` (`deploy/Dockerfile`) | Go binary plus the three web apps it serves: studio at `/`, runner at `/take/`, credential check at `/verify/`. Distroless, non-root. Commands: `migrate`, `serve` |
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
- **Compose:** WAL is archived each minute to the `walarchive` volume. Take a base backup
  nightly (`docker compose exec -u postgres postgres pg_basebackup -D /archive/base-$(date +%F)
  -Ft -z -X none`) and copy the volume to object storage in the same region, encrypted, with a
  35-day lifecycle rule.
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
