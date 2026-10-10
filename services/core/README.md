# core

The Go API server: tenancy, staff sign-in, instruments and versions, the roster,
invitations, candidate sessions, attempts and results. It implements the
`assessmatrix.v1` Connect services in `packages/proto`.

## What it guarantees

| Guarantee | Where it is enforced | Proven by |
| :--- | :--- | :--- |
| Tenant A cannot read or write tenant B's rows | Row-level security on every table, keyed on a transaction-local setting taken from the authenticated principal; the server refuses to start as a role that could bypass it | `TestTenantIsolationAcrossEveryEndpoint`, which fails if any RPC lacks a case, and `TestNoTenantSeesNothing` |
| Every RPC has an access rule | `api.Policy()`; the interceptor refuses unknown procedures | `TestEveryProcedureHasAPolicy` |
| An attempt is tamper-evident | Append-only `attempt_events` (trigger and grants) holding a keyed HMAC hash chain | `TestTamperedAttemptIsDetected` |
| Candidates never receive answer keys | `domain.ForCandidate` on everything served to them | `TestAuthorInviteSitScore` |
| The server clock ends an exam, and offline work done in time is kept | `domain.Counts` | `TestDeadlineIsServerTime`, `TestCountsLateAnswers` |
| Invitation rules from `docs/08` | `internal/api/candidate.go`, `invite.go` | `invite_test.go`, including the revoked-link and 25-wrong-guesses tests |
| Mail goes out only if the change commits, and a link or code is at rest only until sent | Outbox row and River job in the same transaction; the body is cleared on send | Manual check today; see `internal/mail` |

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
go run ./cmd/core tenant "Your Org" you@example.id

AM_DATABASE_URL=postgres://am_app:app@localhost/assessmatrix \
AM_SECRET=$(openssl rand -hex 32) AM_PUBLIC_URL=http://localhost:5180 \
AM_DEV=1 AM_MAILER=mailbox go run ./cmd/core serve
```

Or `docker compose up` from the repo root, after copying `.env.example` to `.env`.

With `AM_DEV=1 AM_MAILER=mailbox`, mail is kept in memory and readable at
`GET /dev/mailbox`. Production uses `AM_MAILER=smtp`; `log` redacts and drops.

## Test it

```bash
export AM_TEST_DATABASE_URL=postgres://am_app:app@localhost/assessmatrix_test
export AM_TEST_MIGRATE_DATABASE_URL=postgres://am_owner:owner@localhost/assessmatrix_test
go test -race ./...
```

Without those variables the database tests skip locally and fail in CI.

## Configuration

| Variable | Meaning |
| :--- | :--- |
| `AM_DATABASE_URL` | The application role. Must own nothing and lack `BYPASSRLS` |
| `AM_MIGRATE_DATABASE_URL` | The schema owner, for `migrate` and `tenant` only |
| `AM_APP_ROLE` | Role `migrate` grants to (default `am_app`) |
| `AM_SECRET` | 32+ random bytes, hex or base64. Rotating it invalidates every link, code and session |
| `AM_PUBLIC_URL` | The origin links point at. Must be https outside development |
| `AM_MAILER` | `smtp`, `log`, or `mailbox` (development only) |
| `AM_SMTP_ADDR`, `AM_SMTP_FROM`, `AM_SMTP_USER`, `AM_SMTP_PASS` | SMTP relay |
| `AM_STATIC_DIR` | Serve the built web app from here, on the same origin |
| `AM_TRUST_PROXY` | `1` behind a proxy that sets `X-Forwarded-For` |
| `AM_DEV` | `1` allows http cookies and the dev mailbox |
