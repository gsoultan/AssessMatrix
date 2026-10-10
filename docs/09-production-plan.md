# Production Plan

From today's prototype to a first real, paid, high-stakes exam. This plan adds no new
architecture: every decision it relies on is already made in `03`–`08` and `AGENTS.md`. What
it adds is **order**, **exit gates**, and the list of things that are not production yet.

## 0. Where we started (2026-10-10)

| Area | State |
| :--- | :--- |
| Strategy, architecture, integration, frontend, invitations | Written (`docs/01`–`06`, `08`) |
| `app/` | React 19 · Mantine 9.6 · Tailwind 4 · Vite 8 · Bun SPA. Six functional screens (Library, Editor, Take exam, People, Assignments, Responses) and seven fixture-driven mockups |
| Persistence | `localStorage` via `src/store.ts` and `src/invite.ts`, shaped like the future API |
| Backend | None. No Go, no Postgres, no proto |
| Tests | Type-check, build, one Playwright smoke (`make e2e`) |
| Source control | Local repo with **no commits**; `github.com/gsoultan/AssessMatrix` is empty |
| Loose ends | `docs/07-mockups.md` is referenced by README, `05` and `06` but missing. Root README says eleven screens, `app/README.md` says thirteen. `package.json` is still `@assessmatrix/mockup` |

### Progress

| Phase | State |
| :--- | :--- |
| 0 · Repo, CI, monorepo | Done. `main` on GitHub; CI runs web, proto, core and full-stack jobs |
| 1 · Proto contract | Done. `packages/proto`, lint and breaking checks in CI |
| 2 · Go core | Done. Postgres with row-level security, hash-chained attempts, outbox mail; tenant isolation proven per RPC |
| 3 · Studio on the core | In progress. Every functional screen runs on the core (`VITE_BACKEND=core`), staff sign in by emailed code, candidates by link, mock-ups hidden; `make e2e-core` proves the loop against Postgres. Media storage and QTI next |
| 4–6 | Not started |

**Nothing in the prototype is a security boundary.** Whoever holds the device can edit
`localStorage`: invite tokens, OTP checks, scores and the attempt log are all forgeable today.
Production readiness is mostly moving those checks behind a server, in the order below.

## 1. What "production ready" means here

Per `04-roadmap.md`, the first production is **one real certification exam, sat by real
candidates, for a design partner who accepts the results.** Not self-serve, not four
surfaces. It is ready when every gate in §4 passes. Everything in `04` §2's "explicitly
deferred" list stays deferred: code execution, AI-in-the-loop tasks, IRT/CAT, Tier 3
biometrics, ATS/LTI, billing, and the Hire / Campus / Grow surfaces.

## 2. The phases

Each phase ends on a gate that is a test or a demo, never a feeling. Phases 2–6 map onto the
12-week MVP in `04` §2; the week numbers are that roadmap's, not a new estimate.

### Phase 0 — Make the repo real (before MVP week 1)

1. First commit and push to `gsoultan/AssessMatrix` on `main`. Keep `.serena/` out of the
   repo or move its decisions into `docs/` (they duplicate `03`).
2. CI on every PR: `make check`, `make build`, `make e2e` (headless Chromium).
3. Restructure into the monorepo in `06` §2: `apps/studio` (today's `app/`), `packages/proto`,
   `packages/ui`, `packages/item-render`, `services/core`. `apps/runner`, `apps/embed` and
   `apps/verify` are created when their phase starts, not before.
4. Restore or remove `docs/07-mockups.md`; fix the screen count; rename the package.
5. Verify the TypeScript 7.0.2 + `connect-es` + ESLint chain (`06` §8 names this as a week-1
   scheduling risk) and pin a fallback if it fails.

**Gate:** a PR to `main` runs green CI that includes the browser smoke test.

### Phase 1 — Contract first: the proto schema

`store.ts` and `invite.ts` already define the API surface. Turn them into
`assessmatrix.v1` protos before writing Go, so the schema is the source of truth (`05` §1).

| Service | Comes from | Methods (first cut) |
| :--- | :--- | :--- |
| `ExamService` | `store.ts` exams + versions | List, Get, Save, Publish, Archive, Restore, Delete, ListVersions |
| `PersonService` | `store.ts` people, `roster.ts` | List, Save, Delete, Import (preview + commit) |
| `AssignmentService` | `store.ts` assignments | Assign, List, Update, Unassign |
| `InviteService` | `invite.ts` | Send, Revoke, Open, RequestCode, VerifyCode, RequestNewLink |
| `AttemptService` | `store.ts` attempts | Start, AppendEvents (batched, idempotency key), Submit, Get |
| `ResultService` | `store.ts` score / aggregate | Score, Aggregate, ListResponses |

`buf lint` and `buf breaking` in CI from the first commit of this package.

**Gate:** `connect-es` client generated and type-checks inside `apps/studio`.

### Phase 2 — Go core foundations (MVP weeks 1–2)

- Go core skeleton: config, graceful shutdown, structured logs with request and trace IDs,
  `/healthz` and `/readyz` that actually check Postgres.
- Postgres with plain-SQL forward migrations; **RLS on every tenant table**, tenant GUC set in
  middleware from the authenticated principal, never from a request field.
- Staff authentication: session cookie (`HttpOnly`, `Secure`, `SameSite=Strict`). OIDC is the
  enterprise gate in `05` §5 and can follow; start with email + one-time code so there is no
  password store to protect.
- **Event-sourced attempts**: `attempt_events(attempt_id, seq, server_ts, kind, payload,
  prev_hash)` append-only, unique `(attempt_id, seq)`. Hash-chaining each event to the last
  is what makes "tamper-evident" in `03` §1 true rather than claimed.
- Audit log table, append-only, for every staff write.
- River for jobs; an outbox table written in the same transaction as the business row.

**Gate (from `04`):** a CI test proves tenant A cannot read or write tenant B's rows through
**any** endpoint. It iterates over every registered RPC, so a new endpoint without a tenant
scope fails the build.

### Phase 3 — Swap the store, screen by screen (MVP weeks 3–5)

Replace `localStorage` behind `store.ts` with Connect calls, one domain at a time, keeping the
screens untouched as the README promises. Order: exams and versions → people → assignments →
invitations → attempts → results.

- **Invitations** follow the table in `08` §4 exactly: HMAC-SHA-256 code hash with a server
  secret, limits enforced in a transaction plus per-IP and per-tenant rate limits, the session
  in a cookie bound to the assignment, mail through the outbox. Delete both stubs (the outbox
  panel and the candidate preview code bar).
- **Media**: presigned uploads to S3-compatible storage, keeping the client-side 1400px WebP
  ingest. The bucket is chosen by tenant region in the storage client (`03` §6). Before
  committing to R2 for Indonesian tenants, confirm it can pin data inside Indonesia; if not,
  run MinIO or an Indonesia-region provider for those tenants.
- **Item engine**: QTI 3.0 import/export for the subset in `04` (choice, text entry, ordering,
  match, extended text). Parameterized generative items are the product thesis but not needed
  for the first exam; schedule them right after if the partner's exam does not need them.
- Fixture-driven screens (AI task, Evidence profile, Integration hub, Embed studio, Event
  inspector, Proctor monitor, Item studio) are hidden from production builds behind a flag.

**Gates:** the two `08` §4 tests (revoked or replaced link cannot start or submit; 25 wrong
guesses lock the link) pass against the server; a QTI file round-trips without loss.

### Phase 4 — Candidate runtime that cannot lose work (MVP weeks 6–8)

The `delivery` profile owns this phase, with `integrity` as Challenger. It is the phase that
protects against "the worst bug this system can have."

- Split `apps/runner` onto its own origin; installable PWA.
- Service worker: app shell, section prefetch, Connect request queue, Background Sync.
- Event-log worker writing to OPFS; responses commit locally first, upload as batched unary
  with idempotency keys (`06` §3).
- SharedWorker attempt coordinator: one attempt, one tab; a second tab is refused and logged.
- Server-authoritative clock with drift correction; the local timer is display only.
- Honest sync state ("Saved locally · 3 queued") wired to the real queue.
- WCAG 2.2 AA: axe in CI, a keyboard path through every item type.
- Budgets in CI: runner ≤ 180 KB gzipped initial JS, no long task over 50 ms during an attempt.

**Gate (from `04`):** an attempt survives a browser kill and a 5-minute network outage with
zero data loss, in an automated test, against the real backend.

### Phase 5 — Integrity tiers 0–2 (MVP weeks 9–10)

- Lockdown, device fingerprint, focus/blur, paste and timing signals into the attempt log.
- On-device vision events (presence, second person) only; no media upload below Tier 3.
- Deterministic rules first. Every flag type ships with its plain-language reason.
- Consent rows per purpose, shown before the start button (already designed in the runner).
- Proctor monitor goes live over Connect server-streaming.

**Gates (from `AGENTS.md`):** a replay of a tampered attempt is still detected; every flag
type renders a reason a candidate could contest.

### Phase 6 — Results, credentials, pilot (MVP weeks 11–12)

- Server-side scoring from the event log; essays to a human review queue.
- Score reports and CTT item analysis (p-value, point-biserial, distractor analysis), run by
  the Python psychometrics worker, never in a request path. No calibration claims beyond CTT.
- Open Badges 3.0 issuance and `apps/verify` (public, no auth, statically cheap).
- Dry run with internal candidates, then the design partner's live exam.

**Gate:** the partner accepts the results in writing.

## 3. Running in parallel from Phase 2 onward

These are not a final phase. Leaving them until the end is how a pilot slips.

| Track | What | Owner profile |
| :--- | :--- | :--- |
| **Deploy** | One `docker-compose` (core, Postgres, MinIO, psychometrics worker) that is also the self-host artifact; same images on k3s for cloud. Staging from Phase 2, production by Phase 5 | perf/cost |
| **Data safety** | Automated Postgres backups with point-in-time recovery, and a restore drill that actually restores | delivery |
| **Observability** | RED metrics per RPC, traces through the event-append path, alerts on sync-queue age and failed appends | delivery |
| **Privacy (UU PDP)** | Tenant `region`; PII store separate from responses, joined by pseudonymous ID; retention job with deletion audit; crypto-shredding for erasure; DPIA drafted before any camera signal ships | privacy |
| **Security** | Secrets out of the repo and env files; dependency scanning; rate limits; an external pentest of auth, invitations and tenancy before the live exam | sec |
| **Psychometrics** | A psychometrician engaged before the first Certify conversation (`04` risk #2) | psychometrics |
| **Load** | Simulate the partner's cohort (all candidates starting in the same minute, autosaving) against staging, with headroom | perf/cost |

## 4. Go-live checklist

- [ ] Tenant-isolation test covers every RPC and is green.
- [ ] Kill-the-browser and network-partition tests pass against production-like infrastructure.
- [ ] Invite tests from `08` §4 pass, and the kill-the-browser test runs with the real session cookie.
- [ ] Tampered-attempt replay is detected.
- [ ] axe clean; keyboard path through every item type.
- [ ] Backup restore drill done on the production database size.
- [ ] Load test at the cohort size plus headroom.
- [ ] Pentest findings closed or accepted in writing.
- [ ] DPIA, consent text, retention rules and DPO contact published.
- [ ] Incident runbook: who is paged, how an in-progress exam is paused and resumed, how a
      candidate's attempt is recovered from the log.
- [ ] Mock and fixture screens absent from the production build.

## 5. Decisions this plan picks by default

Change any of these before Phase 2 starts; after that they are migrations.

| Question | Default | Why |
| :--- | :--- | :--- |
| Staff sign-in for the pilot | Email + one-time code, OIDC next | No password store; reuses the invite OTP machinery |
| Migration tool | Plain SQL, forward-only, run at deploy | Boring, reviewable, matches `migration-safety` discipline |
| Cloud for the pilot | One Indonesia-region VM running the compose stack, managed Postgres if available in-region | Residency is the beachhead's moat; cheap-infra constraint |
| Email provider | Any transactional provider behind the outbox | Swappable; the outbox is the contract |
| Production surface | Certify only; Studio + Runner + Verify | `04` §1 sequencing |

## 6. First step

Phase 0, item 1: make the first commit, push `main` to `gsoultan/AssessMatrix`, and add CI
running `check`, `build` and `e2e`. Every later phase lands as reviewable PRs against it.
