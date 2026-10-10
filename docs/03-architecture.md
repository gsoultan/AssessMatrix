# Architecture

Constraints given: **Go backend**, **TypeScript/Node full-stack**, **multi-tenant SaaS**,
**must run self-hosted on cheap infra**. Those pull in compatible directions if we keep the
service count low and avoid managed-service lock-in.

## 1. The one decision that matters most: event-sourced attempts

**An attempt is an append-only event log, not a row that gets updated.**

Every action — item served, keystroke batch, paste, focus lost, answer changed, code run,
test failed, proctoring signal, submit — is an immutable event with a monotonic sequence and
a server timestamp.

This single choice serves five requirements at once:

| Requirement | How the event log serves it |
| :--- | :--- |
| Legal audit trail | Tamper-evident record for certification disputes and appeals |
| Process evidence | The anti-AI signal *is* the trajectory (see `02-concept.md`) |
| Psychometrics | Response times, revisions, and item exposure fall out for free |
| Crash/offline resume | Replay the log to reconstruct exact state |
| Dispute resolution | Replay an attempt visually, frame by frame |

Get this right in week 1. Retrofitting it later means rewriting everything.

## 2. Service topology

Five deployables. Resist adding a sixth.

```
                         ┌───────────────────────────┐
  Candidate (browser) ───┤  web  (Vite/React19/TS7)  │  candidate runtime, author studio,
  Author / Admin     ────┤       offline-capable     │  admin consoles
                         └─────────────┬─────────────┘
                                       │ Connect RPC v3 (HTTP/1.1 + HTTP/2)
                         ┌─────────────┴─────────────┐
                         │  core  (Go)               │  tenancy, auth, items, delivery,
                         │                           │  attempts, scoring, credentials,
                         │                           │  billing, webhooks, LTI, QTI
                         └───┬────────┬─────────┬────┘
                             │        │         │
              ┌──────────────┘        │         └──────────────┐
    ┌─────────┴─────────┐   ┌─────────┴────────┐   ┌───────────┴──────────┐
    │ runner (Go)       │   │ psychometrics    │   │ Postgres + S3-compat │
    │ sandbox supervisor│   │ (Python worker)  │   │ (MinIO / R2)         │
    │ → isolated nodes  │   │ IRT, CAT, bias   │   └──────────────────────┘
    └───────────────────┘   └──────────────────┘
```

**Why Python for psychometrics, not Go:** IRT calibration, CAT item selection and adverse-
impact analysis live in the scientific Python/R ecosystem (`py-irt`, `catsim`, `irtQ`).
Rewriting them in Go is months of work to reproduce results you'd have to validate anyway,
and certification bodies will ask which published method you used. Keep it a small async
worker with a narrow job interface — it never sits in a request path.

## 3. Stack decisions

| Concern | Choice | Why |
| :--- | :--- | :--- |
| Backend | **Go** | Long-lived delivery connections, sandbox supervision, cheap per-request footprint, single static binary → trivial self-host |
| Frontend | **Vite 8 + React 19 + TypeScript 7** (SPA, not SSR) | The candidate runtime must run *disconnected*. Server-rendered frameworks are the wrong shape for an offline PWA. See `06-frontend-architecture.md` |
| Database | **Postgres**, single instance | Source of truth for everything. `pgvector` later for semantic item search |
| Multi-tenancy | **Row-level security + `tenant_id`**, not schema-per-tenant | Schema-per-tenant dies at ~500 tenants (migration time, connection pressure). RLS is one migration path and one pool. Enforce the tenant GUC in middleware and test the isolation in CI |
| Queue | **River** (Postgres-backed, Go) | A job queue you already have a database for. Adding Kafka/Redis to a cheap-infra product is a self-inflicted wound. Revisit only when a queue is the bottleneck |
| Object storage | **S3-compatible** — MinIO self-hosted, Cloudflare R2 for cloud | R2 has zero egress fees, which matters enormously for proctoring media |
| Realtime | **Connect server-streaming** for monitoring, **WebRTC** only for Tier 3 media | One protocol for unary and streaming, browser-callable with no proxy. Don't reach for WebSockets by reflex |
| Deployment | One `docker-compose` for self-host, same images on k3s for cloud | Self-host must be a first-class artifact, not an afterthought — it's the sovereign/LSP sales motion |

## 4. Code execution — the highest-risk subsystem

In a multi-tenant SaaS, **one sandbox escape is company-ending.** Treat this as a security
system that happens to run code, not a feature.

Two tiers, different technology:

**Tier A — graded submissions** (run tests, return a verdict; seconds)
- Judge0 or Piston, wrapped behind our own Go supervisor that owns queueing, quotas,
  per-tenant rate limits and abuse detection.
- **Judge0 requires `--privileged` Docker and has a history of sandbox-escape CVEs**
  (patched in v1.13.1, April 2024). Therefore: it never runs on the API host. It runs on a
  dedicated, disposable, network-egress-denied worker pool with no cloud credentials, no
  metadata endpoint, and nodes recycled on a schedule. Assume it will be escaped; make the
  escape worthless.
- `nsjail` (namespaces + cgroups + rlimits + seccomp-bpf) is the right primitive if we build
  the runner ourselves rather than adopting Judge0's image.

**Tier B — interactive AI-in-the-loop sessions** (multi-file project, minutes to an hour)
- Judge0's per-submission CPU caps (2–15s) make it structurally unsuitable here.
- Needs a **microVM per session** (Firecracker, or gVisor as a lighter compromise). This is
  the expensive tier. Meter it, price it at Tier 2/3, and don't offer it on free.

## 5. Integrity pipeline

```
browser (on-device ONNX/MediaPipe)  ──events──▶  core  ──▶  attempt event log
   face presence · gaze · second person             │
   focus/blur · paste · device fingerprint          ├──▶  integrity scorer
   keystroke dynamics · timing                      │       (rules → model later)
                                                    └──▶  human review queue (Tier 3)
   keyframes uploaded ONLY at Tier 3 ──────────────────▶  S3, retention-bounded
```

Rules before models. Ship deterministic, explainable rules first — every flag must be
explainable to a candidate who contests it, and "the model said so" is not a defensible
position in an appeal or under the EU AI Act.

## 6. Data protection by design (UU PDP)

Not a compliance checkbox — an architectural constraint, because biometrics are *sensitive
personal data* under UU PDP No. 27/2022 and trigger DPIA + DPO obligations.

- **Tenant-level data residency**: a `region` on the tenant; Indonesian tenants' PII and
  media never leave the ID region. Enforce at the storage-client layer, not by convention.
- **Separate the PII store** from the response/psychometrics store; join by pseudonymous ID.
  Analytics and research queries then never touch identifiable data.
- **Per-purpose consent records** as first-class rows (proctoring, biometric matching,
  recording, AI scoring) with timestamps and versioned policy text. Withdrawal must be
  executable, not theoretical.
- **Retention policies per integrity tier**, enforced by a scheduled job with a deletion
  audit trail. Default proctoring media retention: 90 days, tenant-configurable down.
- **Crypto-shredding** for erasure requests: per-subject keys, destroy the key.

## 7. Standards surface (build as adapters, not core)

| Standard | Where | When |
| :--- | :--- | :--- |
| QTI 3.0 import/export | Item Engine | MVP — it's the anti-lock-in pitch |
| LTI 1.3 + Deep Linking + AGS | Adapter | Phase 3 (Campus) |
| Open Badges 3.0 / W3C VC | Credential service | MVP-adjacent (Certify) |
| xAPI / Caliper | Analytics egress | Phase 4 |
| WCAG 2.2 AA | Candidate runtime | **MVP — non-negotiable.** High-stakes assessment has accommodation obligations, and retrofitting accessibility is far more expensive than building it in |

## 8. What not to build

- Your own LMS. Integrate via LTI; the LMS is the channel, not the competitor.
- Your own video conferencing. Tier 3 live proctoring can ride an existing provider.
- A general-purpose AI agent sandbox. Scope the runner to assessment workloads only.
- Kafka, a service mesh, or microservice decomposition before revenue.
