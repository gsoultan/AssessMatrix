# Frontend Architecture

Stack as specified: **Go 1.27.1 · Bun · React 19 · TypeScript 7.0.2 · Mantine 9.6 ·
Tailwind CSS 4 · Vite 8 · gRPC / Connect RPC v3 · PWA + service worker + web workers ·
mobile-first, excellent on desktop.**

## 1. React client SPA — no Next.js, and why that's correct here

The candidate runtime's hardest requirement is that **it keeps working when the network
doesn't**. A student in a school with intermittent connectivity must be able to sit a
45-minute exam through a 5-minute outage and lose nothing.

A server-rendering framework optimises the first paint of a connected page. That is the
wrong problem. What we need is: ship the whole runtime once, cache it in a service worker,
and run it locally against a local event log, syncing opportunistically. That is a **client
SPA**, built by Vite 8, and it is simpler, smaller and more debuggable than the SSR version
of the same thing.

Consequences, stated plainly:
- No SSR, no RSC, no server actions. Data comes from Connect RPC.
- SEO is irrelevant — every screen is behind auth or a one-time token.
- The marketing site is a separate static thing and should not constrain this decision.

## 2. Applications

Four build targets, one design system.

| App | What | Why separate |
| :--- | :--- | :--- |
| `runner` | Candidate runtime. PWA, offline-first, installable | Different threat model, different caching, must stay small. Its own origin |
| `studio` | Author studio, admin, proctor monitor, integration hub | Rich desktop tooling; size budget is generous |
| `embed` | The `<assessmatrix-runner>` loader + iframe host | Must be tiny and immutably cached; ships to sites we don't control |
| `verify` | Public credential verification | No auth, must be permanently cheap to serve |

```
apps/     runner/  studio/  embed/  verify/
packages/ proto/        buf config + connect-es generated client
          ui/           design tokens, Mantine theme, Tailwind preset, primitives
          workers/      event-log · vision · sync · coordinator
          item-render/  item type renderers (shared runner ↔ studio preview)
services/ core/ (Go)  runner/ (Go)  psychometrics/ (Python)
```

`item-render` being shared is what makes "preview exactly what the candidate sees" true in
the author studio rather than aspirational.

## 3. Worker topology — the part that makes this product possible

The main thread renders. **Everything that could stutter an exam runs off it.**

```
        ┌──────────────── main thread · React 19 ─────────────────┐
        │  Mantine 9 components · Tailwind 4 layout · item render │
        │  a11y focus mgmt · timer UI                             │
        └──┬───────────────┬──────────────┬──────────────────────┘
           │               │              │  postMessage / Comlink
  ┌────────▼───────┐ ┌─────▼──────────┐ ┌─▼─────────────────┐
  │ event-log      │ │ vision worker  │ │ scoring worker    │
  │ worker         │ │ ONNX/MediaPipe │ │ local validation, │
  │ append·batch·  │ │ face presence, │ │ instant feedback  │
  │ compress·      │ │ gaze, 2nd      │ │ for Tier 0/1      │
  │ encrypt → OPFS │ │ person.        │ └───────────────────┘
  │                │ │ Adaptive       │
  │ never blocks   │ │ sampling for   │
  │ keystrokes     │ │ battery        │
  └────────┬───────┘ └─────┬──────────┘
           │               │
      ┌────▼───────────────▼──────────────────────────┐
      │ SharedWorker · attempt coordinator            │
      │ one attempt owns one tab; a second tab is     │
      │ refused AND recorded as an integrity signal   │
      └────────────────────┬──────────────────────────┘
                           │
      ┌────────────────────▼──────────────────────────┐
      │ Service Worker                                │
      │ app shell cache · section prefetch ·          │
      │ Connect request queue · Background Sync       │
      └────────────────────┬──────────────────────────┘
                           │ Connect RPC v3, batched unary + idempotency key
                    ┌──────▼──────┐
                    │  Go core    │
                    └─────────────┘
```

Three decisions inside that diagram worth defending:

- **The event log lives in a dedicated worker writing to OPFS**, not in React state and not
  on the main thread. Event volume is high (keystroke batches, focus changes, proctoring
  signals). OPFS handles binary and append patterns far better than IndexedDB, and the worker
  means an fsync never costs a dropped keypress.
- **A SharedWorker owns the attempt.** It prevents the same attempt running in two tabs — and
  the attempted second tab is itself an integrity event. One mechanism, two wins.
- **Batched unary, not streaming, for event upload.** A broken stream on a flaky mobile
  connection loses the batch and complicates resume. Idempotent batched unary retries
  trivially. Reserve Connect **server-streaming** for the direction that actually suits it:
  the proctor monitor watching live attempts.

## 3a. Media ingest happens on the client

Images are downscaled to a 1400px long edge and re-encoded (WebP, q0.85) in a canvas *before*
they are ever stored or uploaded. Three reasons, in order of importance:

1. **Bandwidth.** The candidate side of this product runs on mid-range Android over weak
   connections. Shipping an author's untouched 4 MB phone photo into an exam is a defect.
2. **Cost.** Proctoring media already dominates storage and egress; item media should not add
   to it for no gain.
3. **Quota.** Whatever the local buffer is — `localStorage` in the prototype, OPFS in the real
   runner — originals exhaust it fast.

Storage failures must be *reported*. Silently dropping an author's item media (or a
candidate's attachment) is the same class of bug as losing an in-progress attempt.

**Alt text is a required field, not a nicety.** High-stakes assessment carries accommodation
obligations; a figure with no text alternative excludes screen-reader candidates from the
item. The one subtlety worth encoding in the UI copy: the description must not give the answer
away, which is a real tension on "identify the correct diagram" items.

## 4. Offline and sync model

- **Prefetch by section, not by item.** On section entry, pull every item, asset and rubric
  for that section. The candidate can then complete the whole section offline.
- **Local-first writes.** Responses commit to the local log first and render instantly. Sync
  is a background concern the candidate never waits on.
- **Honest sync UI.** Show `Saved locally · 3 queued`, never a fake green tick. In a
  high-stakes exam, lying about save state is the cruellest possible bug.
- **Server time is authoritative for the clock**, with drift correction on each sync; the
  local timer is display only. Never let a device clock decide when an exam ends.
- **Conflict policy: none.** One attempt, one owner, append-only log — conflicts are
  structurally impossible rather than resolved. This is a direct payoff from event sourcing.
- **Media (Tier 2/3)** buffers to OPFS and uploads on reconnect with resumable chunks; the
  exam never blocks on media upload.

## 5. PWA strategy per app

| App | Service worker role | Installable |
| :--- | :--- | :--- |
| `runner` | Full: app shell, section prefetch, request queue, Background Sync | **Yes** — installed = first-party storage, best offline, fullscreen kiosk feel |
| `studio` | Cache static assets only; no offline authoring in v1 | No |
| `embed` | **None.** Partitioned third-party storage makes it unreliable — see `05-integration.md` §3 | No |
| `verify` | Static cache | No |

Installing the runner as a PWA is the recommended path for schools and certification
centres: it upgrades storage to first-party, unlocks reliable Background Sync, and gives a
distraction-reduced fullscreen surface.

## 6. Mantine 9.6 + Tailwind 4 — make them coexist deliberately

Two styling systems in one app is a real cost. It's worth it if the division of labour is
explicit; it's a mess if it isn't.

**Division of labour:**
- **Mantine** owns *behaviour-heavy components*: Modal, Combobox, DatePicker, Table,
  Notifications, focus traps. These are the things that are expensive and risky to rebuild,
  and where Mantine's accessibility work is the reason to adopt it.
- **Tailwind** owns *layout, spacing, responsive composition and one-off styling*.
- **Never both for the same concern.** A component is Mantine-styled or Tailwind-styled, not
  a negotiation.

**Cascade layers, declared once and never debated again:**
```
@layer tailwind-base, mantine, tailwind-utilities;
```
Mantine's component styles beat Tailwind's preflight reset; Tailwind utilities beat Mantine
component styles, so a utility override actually works without `!important`. Getting this
order wrong is the single most common way this pairing goes bad.

**Verified gotcha — import Mantine from CSS, never from JS.** Declaring the layer order is
not sufficient. CSS layer precedence is decided by *first appearance*, and a JS-side
`import '@mantine/core/styles.layer.css'` is injected by the bundler **ahead of** your
stylesheet. That registers the `mantine` layer before the `@layer` statement can order it,
the order silently becomes `mantine → tw-base → tw-utilities`, and Tailwind's preflight then
strips every Mantine component back to unstyled HTML. It fails quietly: the app builds, the
types pass, and buttons just look like text. The fix is to `@import '@mantine/core/styles.layer.css'`
from inside the stylesheet, directly after the `@layer` statement, and to import nothing from
JS. Confirmed in `app` against Mantine 9.6.2 + Tailwind 4.3.3.

Mantine 9 also emits hashed class names (`m_cf365364`) rather than readable ones, so grepping
the stylesheet for `SegmentedControl` to check whether styles loaded returns nothing. Check
the hashed class from the rendered DOM instead.

**One token layer drives everything.** Design tokens live as CSS custom properties; the
Mantine theme and the Tailwind 4 `@theme` both read from them. This is what makes the embed
story work: a host app posts its theme tokens across the bridge, the runtime sets custom
properties, and Mantine *and* Tailwind both respond — no rebuild, no duplicate theme.

## 7. Mobile-first, excellent on desktop

Not "responsive" as an afterthought — two genuinely different information architectures over
one component set.

| | Mobile (portrait) | Desktop |
| :--- | :--- | :--- |
| Candidate runner | One item per screen; sticky timer + progress; actions in the bottom thumb zone; item navigator as a sheet | Three panes: navigator · item · tools/scratchpad. Keyboard-first |
| Author studio | Review and approve only — authoring is a desktop task, don't pretend otherwise | Full split-pane authoring with live candidate preview |
| Proctor monitor | Single-attempt triage cards | Grid of live attempts + evidence timeline |

Mobile realities to design against, not around: app-switch (`visibilitychange`) is the main
integrity signal since screenshots aren't detectable; front-camera proctoring drains battery,
so sample adaptively and show the battery cost honestly; on-screen keyboards eat half the
viewport, so the answer area must stay visible when focused.

## 8. Budgets and honest notes on the stack

**Budgets** (enforced in CI, because an exam that loads slowly on a mid-range Android in a
regional school is an exam that fails):
- `runner` initial JS ≤ 180 KB gzipped; interactive ≤ 2.5 s on mid-tier Android over 3G.
- `embed` loader ≤ 6 KB gzipped, immutable cache.
- Main-thread long tasks during an attempt: none over 50 ms.
- WCAG 2.2 AA, axe-clean, full keyboard path through every item type.

**Honest notes on the chosen versions:**
- **TypeScript 7** (the native compiler port) is a large type-checking speedup, but type-aware
  lint rules and some codegen tooling historically lag a major compiler rewrite. Verify the
  `connect-es` + Mantine + ESLint chain on 7.0.2 in week 1, and keep a pinned fallback. This
  is a scheduling risk, not a reason to avoid it.
- **Vite 8 + Bun**: use Bun as package manager and test runner; let Vite own the build. Mixing
  bundlers across apps in one monorepo is how build times and bug reports get weird.
- **Four build targets** means the shared `ui` and `item-render` packages must stay strictly
  framework-pure — no app-specific imports leaking in, or `embed` inherits `studio`'s weight.
