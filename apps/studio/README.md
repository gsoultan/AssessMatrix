# AssessMatrix — prototype

Thirteen screens. Six of them are **functional**, not mocked. The sidebar lists the everyday ones,
one line each, in the order the work is done; the design mock-ups sit folded under **More**.

An instrument has a **purpose** — *Exam*, *Quiz* or *Questionnaire* — and that one field decides
whether answers have a right one, whether there is a clock, and what "results" means. The
engine underneath is identical: sections, delivery, autosave, media, event log.

- **Library** — the landing screen: every exam, quiz and questionnaire in one list, with an
  Active / Archived split, type filters, search, status and response counts, and per-row
  Edit / Preview as candidate / Publish / Duplicate / Archive. Creating anything starts here.
- **Editor** — a document, not a control panel. It has no sidebar entry on purpose: you always
  reach it by opening something from the Library, so a standalone nav item would just edit
  whichever instrument happened to be first. Library stays highlighted while you are in it. Questions are cards you expand and edit
  in place; the collapsed card is its own summary. Sections, five question types, reorder,
  duplicate, a Preview mode that renders the whole exam as the participant sees it with an
  optional answer key, and settings tucked behind a toggle because you set them once.
- **People** — the roster you assign from. Add someone inline, or paste a list (bare email,
  `Name <email>`, or `Name, email, group`); duplicate emails are skipped, never doubled.
- **Assignments** — the delivery step. It opens on a list of instruments, each with how many
  people have completed it; filter by Open / All completed / No one invited, or search. Click one
  to open its page: **Invite people** opens a side panel to pick from the roster (or paste
  addresses), and the page below has two tabs, **People** and **Outbox**. Each assignment mints its own invitation token — **one person, one
  link, never a shared URL** — and the row tracks them as **Invited → Opened → In progress →
  Completed** (or **Expired**). *Opened* means they followed the link but have not started.
  Filter the invited list by status or search by name, with the score landing back on the row. A
  submitted row has **View answers**: every question, what that person answered, right or wrong
  against the key of the version they sat, and the points. Anonymous instruments have none.
- **Candidate preview** — the **candidate application**, as an administrator sees it: pick any
  exam (drafts too) and sit it exactly as a candidate would, under a bar that says it is a
  preview. A real candidate never sees this list: their invitation link opens one assessment,
  full-bleed, with no product chrome. The stages are link → emailed code → **start page** →
  attempt → review → submit → result; the start page holds the details, the device check and
  what is collected, so one press begins. Consent is its own step when the tier collects
  anything, and an exam that uses the camera checks it only after consent. A questionnaire ends
  in a thank-you, not a grade.
- **Responses** — aggregate results for questionnaires: per-question distributions, means for
  scales, and free-text answers. One hue for every bar, because the options are categories of a
  single series.

Images work in all three places an assessment needs them: a **figure on the question**,
**images as the answer options** (which auto-switch to a picture grid when every option has
one), and a **participant attachment** on short/essay answers for handwritten working or a
sketch.

Essays are never auto-scored; their points are reported separately as pending human review.

The other seven screens are design mockups driven by fixtures in `src/data.ts`.

## Two backends, chosen at build time

| `VITE_BACKEND` | Data lives in | Who signs in | Mock-up screens |
| :--- | :--- | :--- | :--- |
| unset (default) | this browser's `localStorage` | nobody | shown |
| `core` | the Go core (`services/core`) over Connect | staff by emailed code; candidates by their invitation link | hidden (`VITE_MOCKS=1` shows them) |

Screens never know which one they run on. They read and write through `src/state.ts`, which
calls `src/backend/repo.ts` (staff data) and `src/backend/gate.ts` (the candidate's way in);
each has a local and a core implementation. `src/backend/convert.ts` is the only place that
knows the wire format. A candidate's answers go to the core as events through
`src/backend/sync.ts`, a queue kept in `localStorage` so a dropped connection or a killed
browser loses nothing that reached it.

Against the core, a candidate who opens their link sees the assessment alone: no studio, no
sign-in. The server holds the clock, the answer key and the score; the browser holds none of them.

```bash
make dev      # from the repo root — installs, frees the port, starts on :5180
make check    # type-check
make build    # type-check + production build
make e2e      # browser test of the create → take → score loop, local data
make e2e-core # the same loop against the core and Postgres (see scripts/e2e-core.sh)
```

Or directly: `bun install && bun run dev`. Override the port with `PORT=3000 make dev`.

To develop against the core, run it with `AM_PUBLIC_URL=http://localhost:5180` (see
`services/core/README.md`), then `VITE_BACKEND=core bun run dev`. Vite forwards the API and
the dev mailbox to `CORE_URL` (default `http://localhost:8080`).

## Stack

React 19.3 · TypeScript 7.0.2 · Mantine 9.6.2 · Tailwind CSS 4.3.3 · Vite 8.3 · Zustand 5.0 ·
Bun 1.4 · `@tabler/icons-react`. **React client SPA — no Next.js, no SSR, no RSC**, because the
real runner has to keep working disconnected mid-exam (see `../docs/06-frontend-architecture.md`).

**Bun is the only runtime.** It installs, runs Vite, and executes the Playwright suite —
`bun e2e.mjs`, not `node`. There is no Node in the toolchain.

## Screens

| Screen | Shows |
| :--- | :--- |
| **Library** | *Functional.* Lists every instrument, Active/Archived split, filter by type, search, publish, duplicate, archive, restore, open, preview as candidate. |
| **Editor** | *Functional.* Inline question cards, seven types, reorder/duplicate, whole-exam preview with answer key, publish. |
| **Candidate preview** | *Functional.* The candidate app — link, code, start page, consent (by tier), attempt, review, confirm, result. Timer warnings, autosave, flags, figures, attachments, resume. |
| **People** | *Functional.* Roster, inline add, paste-import with dedupe, groups, search. |
| **Assignments** | *Functional.* Assign by person or group, per-person invitation links, live status and score tracking. |
| **Responses** | *Functional.* Aggregate distributions, means, free-text answers. |
| **AI-in-the-loop task** | The assistant is *given* to the candidate; the collaboration is scored. Transcript, process evidence, rubric. |
| **Evidence profile** | Skill bars with confidence intervals; Open Badges 3.0 credential. |
| **Integration hub** | The Four Doors, connected apps, standards adapters. |
| **Embed studio** | Mode picker, live preview, **preflight**, snippet. |
| **Event inspector** | Webhook deliveries and window replay. |
| **Proctor monitor** | Live attempts, integrity meters, evidence timeline, privacy posture. |
| **Item studio** | Generative item template, variant re-roll, CTT calibration. |

## Why the builder looks the way it does

The first version was a three-pane control panel — settings, a structure tree, an item
editor, a single-item preview, a publish box and an exam list, all on screen at once. It was
six panels competing for attention with no obvious starting point, a drag handle that didn't
drag, and "New exam" buried in the bottom-right corner.

It is now a document: one column, questions as cards you expand in place, the primary action
(*Add question*) sitting where the work is rather than in a side panel. The collapsed card
*is* the summary, so there is no selection state to track and no separate preview pane to
reconcile. Affordances are honest — reorder is two arrow buttons that work, not a handle that
doesn't.

## The candidate point of view

The participant's screens are designed against a different brief from the authoring tools, and
the differences are deliberate:

- **No product chrome.** Full-bleed, with a single reading column. Nothing to navigate away to.
- **Bigger.** 15–18px body type and 48px touch targets, against 12.5px and 28px in the admin UI.
- **Nothing hidden before it matters.** The invitation states the duration, the threshold and
  exactly what will be collected *before* the start button, not after.
- **Consent only when there is something to consent to.** Tier 0/1 skip that stage entirely.
- **A review step before submitting**, with answered / flagged / unanswered counts and a jump
  target for every question — and a confirmation that says plainly it cannot be undone.
- **Time warnings are inline and dismissible** at 5 minutes and 1 minute. A modal that seizes
  the screen when someone is mid-answer makes a stressful moment worse.
- **The save state never lies.** "Tersimpan di perangkat · N menunggu sinkron", never a fake tick.
- **The result shows what was recorded.** "Apa yang kami catat" expands the event log and states
  that no video was uploaded — the privacy claim is shown, not asserted in a policy page.

## Delivery

Publishing does not deliver an instrument — it only makes it *deliverable*. Delivery is an
**assignment**: one person, one instrument, one token, one lifecycle.

- Each assignment mints its **own invitation link**. Sharing one URL across a cohort would make
  the identity on every attempt a guess, which is the whole point of a proctored instrument.
- Opening an invitation identifies the candidate, so the attempt records `personId` — and
  starting or submitting writes straight back to the assignment. The roster shows real progress
  rather than "invited" forever.
- You can assign people to a **draft**, and the screen says plainly that their invitations will
  not open until it is published.
- You can **invite addresses that are not on the roster** — paste them in Assignments and they
  are created and invited in one step. "Off-roster" means you did not have to add them first,
  not that no record exists: an invitation needs a person to hang status and score on, so new
  addresses join the roster.
- Removing someone from the roster cancels their invitations; permanently deleting an
  instrument cancels all of its assignments. No dangling invitations.

## Versions

Every instrument carries a version number, and **publishing is what freezes one**. The frozen
copy is what candidates actually get.

- Editing something live asks before saving: **Save as v(n+1)** — branches a working version,
  leaving the live one untouched — or **Update v(n) in place**, which rewrites what people are
  sitting and says so. The safe option is the visually primary one.
- An **attempt is pinned** to the version it started on, and an **invitation is pinned** to the
  version it was issued against. Editing the working copy cannot rewrite the paper underneath
  someone who is mid-exam. The assignment table shows which version each invitation opens.
- The Library shows `v2 live · v3 draft` when the working copy has moved ahead.

This closes the gap the live-edit warning used to only *describe*.

## Lifecycle

`draft` → `published` → `archived`, and back again.

- A **draft** is invisible to candidates. The candidate picker lists published instruments
  only; previewing a draft from the Library shows a banner saying no candidate can reach it.
- **Publishing is a separate action from saving.** A draft that is merely saved must not go
  live by accident, so the editor has both a *Save draft* and a *Publish* button. Publishing is
  blocked while an instrument has no questions.
- **Archive is the default retirement, not delete.** An instrument carries authored work and,
  once sat, evidence someone may need to produce later. Archived items keep their data, sit in
  their own view, and restore as drafts. Permanent deletion exists — but only from the archive,
  never one click from the main list.
- **Duplicating never publishes.** A copy always starts as a draft.
- Editing a published instrument shows a warning, because changes apply immediately — including
  to anyone mid-attempt. A production build would version instead; the UI says so rather than
  pretending the problem does not exist.

## Question types

`single` · `multiple` · `boolean` · `short` · `essay` · `likert` · `rating`

Likert and rating carry no answer key, so they never enter the auto-scored total — same as
essays, whose points are reported separately as pending human review. Putting a scale question
in a graded exam is allowed; it just isn't machine-scored.

## Images

Every image is downscaled to a 1400px long edge and re-encoded as WebP on ingest — a 2000×1200
PNG lands at about 9 KB. Untouched phone photos would blow the `localStorage` quota on the
second upload, and a 4 MB figure on a mid-range Android in a regional school is its own kind
of failure. The quota is now a reported error rather than a silent one; losing an author's
work without telling them is not acceptable.

`Media` holds a URL, so production changes the field's *value* (an object-storage URL) and not
the schema.

**Alt text is demanded, not suggested.** An item whose figure has no text alternative is
unusable for a screen-reader candidate, and in high-stakes testing that is an accommodation
failure with legal weight. The builder flags missing alt text per image and counts the gaps in
the header. The prompt says to describe the figure *without giving the answer away* — for a
"which diagram is correct?" item, a careless description is an answer key.

## State

`src/store.ts` is persistence (localStorage today, Connect RPC later). `src/state.ts` is a
**Zustand** store holding the in-memory view every screen reads: the exam catalogue, collected
responses, and the live attempt.

The active screen and the selected instrument live there too, because "open this in the editor"
is a navigation *plus* a selection and the two belong together — that is what makes the
Library's Edit and Preview actions work across screens.

The division is deliberate — shared, persisted data goes in the store; view state (which stage,
which question, which panel is open) stays local to the component. Putting ephemeral view state
in a global store is an anti-pattern, not a best practice.

The correctness argument for Zustand here is specific: **its actions run outside React's render
machinery**, so unlike a `setState` updater they are not double-invoked under StrictMode. That
exact hazard already bit this codebase once — `addResponse()` inside a `setAttempt` updater
recorded every questionnaire submission twice. Persisting from an action is structurally safe
in a way that persisting from an updater never was.

## Three things to know before you touch the styles

1. **Mantine is imported from `src/styles.css`, not from `main.tsx`.** Moving it to a JS
   import breaks the cascade layer order and silently unstyles every Mantine component. The
   comment in `styles.css` explains why.
2. **Nothing hardcodes a hex.** Every colour resolves through a CSS custom property in the
   `@theme` block. That single token layer drives Mantine, Tailwind *and* dark mode — and it
   is the same mechanism the embed theme bridge uses to restyle the runner from a host app
   with no rebuild. If you inline a hex, dark mode silently breaks for that element.
3. **Dark mode is a selected palette, not an inversion.** Its steps are drawn from the same
   ramps but stepped for the dark surface, and validated against it — both modes cleared the
   lightness band, chroma floor, CVD separation, normal-vision floor and contrast checks.
   `#1baf7a` (light aqua) would fail the dark lightness band; `#199e70` is used instead.
   Meter tracks go markedly darker in dark mode so the confidence band still separates.
