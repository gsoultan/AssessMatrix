# core

The Go API server: tenancy, staff sign-in, instruments and versions, the roster,
invitations, candidate sessions, attempts and results. It implements the
`assessmatrix.v1` Connect services in `packages/proto`.

## What it guarantees

| Guarantee | Where it is enforced | Proven by |
| :--- | :--- | :--- |
| Tenant A cannot read or write tenant B's rows | Row-level security on every table, keyed on a transaction-local setting taken from the authenticated principal; the server refuses to start as a role that could bypass it. Every foreign key between tenant tables includes `tenant_id`, so a row cannot name another tenant's row even if a handler forgets to check | `TestTenantIsolationAcrossEveryEndpoint`, which calls every RPC (public ones too) from another tenant, as its administrator and as someone who works in both, and fails if any RPC lacks a case; `TestEveryTableIsTenantScoped`, `TestForeignKeysStayInsideATenant` and `TestSecurityDefinerFunctionsAreReviewed`, which read the schema, so a new table, key or RLS-bypassing function fails the build until it is scoped or reviewed; `TestNoTenantSeesNothing` |
| Every RPC has an access rule | `api.Policy()`; the interceptor refuses unknown procedures | `TestEveryProcedureHasAPolicy` |
| An attempt is tamper-evident | Append-only `attempt_events` (trigger and grants) holding a keyed HMAC hash chain | `TestTamperedAttemptIsDetected` |
| Candidates never receive answer keys | `domain.ForCandidate` on everything served to them | `TestAuthorInviteSitScore` |
| The server clock ends an exam, and offline work done in time is kept | `domain.Counts` | `TestDeadlineIsServerTime`, `TestCountsLateAnswers` |
| Invitation rules from `docs/08` | `internal/api/candidate.go`, `invite.go` | `invite_test.go`, including the revoked-link and 25-wrong-guesses tests |
| An image is readable only inside its tenant, and stored only in the tenant's region | `media` rows under row-level security decide every read of `/media/{id}`; uploads are sniffed (WebP, JPEG, PNG only) and refused when the tenant's region is not the store's | `media_test.go` |
| Mail goes out only if the change commits, and a link or code is at rest only until sent | Outbox row and River job in the same transaction; the body is cleared on send | `outbox_test.go`: a rolled-back change queues nothing; a sent invitation is delivered once and leaves no link in the clear |

Links, codes and session cookies are stored only as HMAC-SHA-256 under keys
derived from `AM_SECRET`, one key per purpose.

## Run it

```bash
# Postgres with two roles: an owner for migrations and an unprivileged app role.
psql -c "CREATE ROLE am_owner LOGIN PASSWORD 'owner'" \
     -c "CREATE ROLE am_app LOGIN PASSWORD 'app'" \
     -c "CREATE DATABASE assessmatrix OWNER am_owner"

export AM_MIGRATE_DATABASE_URL=postgres://am_owner:owner@localhost/assessmatrix
go run ./cmd/core migrate
# Either create the first organisation and its administrator here, or skip
# this and use /setup (below). More tenants, and who works in which, are
# managed from the studio (Access, and the tenant menu).
go run ./cmd/core tenant "Your Org" you@example.id

AM_DATABASE_URL=postgres://am_app:app@localhost/assessmatrix \
AM_SECRET=$(openssl rand -hex 32) AM_PUBLIC_URL=http://localhost:5180 \
AM_DEV=1 AM_MAILER=mailbox go run ./cmd/core serve
```

Or `docker compose up` from the repo root, after copying `.env.example` to `.env`.

With `AM_DEV=1 AM_MAILER=mailbox`, mail is kept in memory and readable at
`GET /dev/mailbox`. Production uses `AM_MAILER=smtp`; `log` redacts and drops.

## First run: /setup

On a database with no organisation, `serve` logs a warning with the setup URL and a
`setup_code`. Open `<AM_PUBLIC_URL>/setup` (the sign-in page sends you there too), enter the
code, and name the organisation and its first administrator; you are signed in as that
administrator straight away. `core setup-code` prints the same code; it is derived from
`AM_SECRET`, so every replica agrees on it.

Setup works once. The database makes that so: `am_setup_first_tenant` locks `tenants` and
refuses when any organisation exists, so two racing setups end with one winner, and the code
is worthless afterwards. The serving role cannot create organisations any other way
(`am_create_tenant` is revoked from it). Proven by `TestFirstRunSetupHappensOnce` and
`scripts/e2e-setup.sh`.

## Test it

```bash
export AM_TEST_DATABASE_URL=postgres://am_app:app@localhost/assessmatrix_test
export AM_TEST_MIGRATE_DATABASE_URL=postgres://am_owner:owner@localhost/assessmatrix_test
go test -race ./...
```

The setup test also needs an empty database next to it, named with `_setup` appended
(`CREATE DATABASE assessmatrix_test_setup OWNER am_owner`). Without those variables the
database tests skip locally and fail in CI.

## Configuration

| Variable | Meaning |
| :--- | :--- |
| `AM_DATABASE_URL` | The application role. Must own nothing and lack `BYPASSRLS` |
| `AM_MIGRATE_DATABASE_URL` | The schema owner, for `migrate` and `tenant`. Given to `serve` too, it applies pending migrations before serving and then closes that connection (a single-machine convenience; keep a separate `migrate` step where you can) |
| `AM_APP_ROLE` | Role `migrate` grants to (default `am_app`) |
| `AM_SECRET` | 32+ random bytes, hex or base64. Rotating it invalidates every link, code and session |
| `AM_PUBLIC_URL` | The studio's origin. Must be https outside development |
| `AM_RUNNER_URL` | Where invitation links open the candidate runner (`/take/`). Defaults to `AM_PUBLIC_URL`; set a second hostname for this service to keep candidate and staff cookies apart |
| `AM_MAILER` | `smtp`, `log`, or `mailbox` (development only) |
| `AM_SMTP_ADDR`, `AM_SMTP_FROM`, `AM_SMTP_USER`, `AM_SMTP_PASS` | SMTP relay |
| `AM_STATIC_DIR` | Serve the built web app from here, on the same origin |
| `AM_TRUST_PROXY` | `1` behind a proxy that sets `X-Forwarded-For` |
| `AM_DEV` | `1` allows http cookies and the dev mailbox |
| `AM_MEDIA_STORE` | `fs`, `s3`, or `none` (default; image uploads are refused) |
| `AM_MEDIA_REGION` | The region the store is in (default `id`). Tenants whose `region` differs cannot upload |
| `AM_MEDIA_DIR` | Directory for `fs` |
| `AM_S3_ENDPOINT`, `AM_S3_BUCKET`, `AM_S3_ACCESS_KEY`, `AM_S3_SECRET_KEY` | Any S3-compatible store for `s3`. `AM_S3_INSECURE=1` allows plain http in development |
