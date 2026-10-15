# AssessMatrix

**One assessment engine. Four surfaces. Evidence that travels.**

AssessMatrix measures a skill once and lets that evidence move with the person —
from a school exam, to a professional certification, to a hiring screen, to a
workplace skills review — as a portable, verifiable credential.

## Why now

The incumbent assessment industry is built on two assets that are both depreciating fast:

1. **Large static item banks.** Every question in a public bank is now solved by an LLM
   in seconds. Banks leak, and a leaked bank is a worthless bank.
2. **Cheating detection.** In 2026, 48% of technical candidates were flagged for AI
   assistance across 19,368 live interviews — and 61% of those flagged would still have
   advanced past the hiring bar. Proctored coding fraud doubled from 16% to 35% in one
   year. For asynchronous unproctored assessments, estimated true fraud is 60–80%.

Detection is a losing arms race. You cannot win it by watching harder.

AssessMatrix's bet: **make assessments AI-immune by construction, not by surveillance.**

- Items are *generated per candidate* from calibrated parameterized templates — there is
  no static bank to leak.
- Scoring uses **process evidence** (the edit trajectory, the debugging path, the revisions)
  not just the final artifact. A pasted LLM answer has no process.
- Hard tasks are **AI-in-the-loop by design**: the candidate gets the AI, and we grade how
  they direct, verify and correct it — which is the actual job now.

## Repo map

| Path | What |
| :--- | :--- |
| `docs/01-market-and-competition.md` | Competitive comparison, market sizing, sourced findings |
| `docs/02-concept.md` | Product concept, the Matrix, four surfaces, pricing model |
| `docs/03-architecture.md` | Technical architecture and stack decisions |
| `docs/04-roadmap.md` | Phased plan, 12-week MVP scope, risk register |
| `docs/05-integration.md` | How other applications integrate — the Four Doors, schema fabric, events |
| `docs/06-frontend-architecture.md` | Stack, worker topology, PWA/offline, Mantine+Tailwind, monorepo |
| `docs/08-invite-links.md` | Candidate invitations — roster import, the secured link, what moves to the backend |
| `docs/09-production-plan.md` | Phased plan from prototype to the first live exam, with exit gates |
| `AGENTS.md` | Developer profile roster (Driver/Challenger) |
| `apps/studio/` | **Running prototype** — React 19 · Mantine 9.6 · Tailwind 4 · Vite 8 · Bun |
| `scripts/` · `Makefile` | Dev scripts — `make up`, `make dev`, `make check`, `make build`, `make e2e` |

## Status

Strategy and architecture defined. `apps/studio/` runs thirteen screens, six of them functional; the exam authoring and
delivery loop, and candidate invitations by emailed link, are functional against local storage. No backend exists yet.

## Run it locally

One command brings up the whole stack: Postgres, the schema, a demo admin, the Go core and
the web apps, with hot reload.

```bash
make up       # Ctrl-C stops it; `make down` stops Postgres too
```

On a Mac you need [Docker Desktop](https://www.docker.com/products/docker-desktop/) or
[OrbStack](https://orbstack.dev) running, plus `brew install go` and
`curl -fsSL https://bun.sh/install | bash`. The first run creates `.env` with fresh secrets,
installs packages and downloads the Go toolchain the core pins, so it takes a few minutes.

| What | Where |
| :--- | :--- |
| Studio | http://localhost:5180, sign in as `admin@example.id` |
| The sign-in code | http://localhost:5180/dev/mailbox?to=admin@example.id (mail stays in memory; nothing is sent) |
| Candidate app | http://localhost:5180/take/, opened from an invitation link in the same mailbox |
| Verify page | http://localhost:5181/verify/ |
| Core API | http://localhost:8080, metrics on :9090 |
| Postgres | `localhost:5432`, database `assessmatrix`; passwords in `.env` |

Edits to the studio or the verify page show in the browser at once. Edits to the core (any
`.go` or `.sql` file) rebuild it, apply new migrations and restart it; a build error leaves the
previous build serving. The psychometrics worker runs too if `python3` can import `psycopg`
(`pip3 install -r services/psychometrics/requirements.txt`).

If another Postgres already holds port 5432, add `AM_PG_PORT=5433` to `.env`. Other ports:
`PORT`, `VERIFY_PORT`, `CORE_PORT`, `METRICS_PORT`. `AM_DEMO_EMAIL` changes the demo admin.
`RESET=1 make down` deletes the database, so the next `make up` starts empty.

The other commands:

```bash
make dev      # the studio alone on local storage, no backend: http://localhost:5180
make check    # type-check
make build    # type-check + production build
make e2e      # browser smoke test of create-exam → take-exam → score
make          # list everything
```

`Create exam` → `Candidate preview` is a **working loop**: author sections and items, publish, sit a
live attempt with a running timer and per-keystroke autosave, submit, get auto-scored, and
resume after a reload. Exams and attempts persist in `localStorage`; `src/store.ts` is shaped
so swapping it for Connect RPC calls should not require touching a screen.
