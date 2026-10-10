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
| `docs/07-mockups.md` | Wireframes — integration surfaces, candidate runner, proctor monitor |
| `docs/08-invite-links.md` | Candidate invitations — roster import, the secured link, what moves to the backend |
| `AGENTS.md` | Developer profile roster (Driver/Challenger) |
| `app/` | **Running prototype** — React 19 · Mantine 9.6 · Tailwind 4 · Vite 8 · Bun |
| `scripts/` · `Makefile` | Dev scripts — `make dev`, `make check`, `make build`, `make e2e` |

## Status

Strategy and architecture defined. `app/` runs eleven screens; the exam authoring and
delivery loop, and candidate invitations by emailed link, are functional against local storage. No backend exists yet.

```bash
make dev      # install if needed, free port 5180, start on http://localhost:5180
make check    # type-check
make build    # type-check + production build
make e2e      # browser smoke test of create-exam → take-exam → score
make          # list everything
```

`Create exam` → `Take exam` is a **working loop**: author sections and items, publish, sit a
live attempt with a running timer and per-keystroke autosave, submit, get auto-scored, and
resume after a reload. Exams and attempts persist in `localStorage`; `src/store.ts` is shaped
so swapping it for Connect RPC calls should not require touching a screen.
