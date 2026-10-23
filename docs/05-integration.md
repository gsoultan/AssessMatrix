# Integration Architecture — "AssessMatrix inside your app"

Brainstorm document. No code. Decisions here shape the public surface, so they are expensive
to change later.

## 1. Principle: one schema, many doors

The integration story is not "we have an API." It's that **the same protobuf schema powers
our own frontend, our partners' backends, and the embedded runtime** — so there is no
second-class public API that drifts from the internal one. If our own author studio can do
it, a partner can do it, through the same contract.

```
                       assessmatrix.v1.proto   ← single source of truth
                                 │  (buf: lint + breaking-change CI)
     ┌──────────────┬────────────┼─────────────┬──────────────┬─────────────┐
     ▼              ▼            ▼             ▼              ▼             ▼
  connect-go     connect-es   partner SDKs   OpenAPI/      webhook       reference
  (Go core)      (our React   (py·java·php·  REST gateway  payload        docs
                  client)      node·dotnet)  (for the      schemas       (buf registry)
                                             REST-only)
```

**Connect RPC v3 is the right substrate** specifically because it speaks the Connect
protocol, gRPC, and gRPC-Web on one endpoint. The browser calls it directly with `fetch` —
no Envoy, no grpc-web proxy, no sidecar. That is what makes an embeddable runtime practical
on cheap infra.

## 2. The Four Doors

Integration depth is a spectrum. Publish it as one, so a customer can start in ten minutes
and go deeper without re-platforming.

| | Door | How it works | Host effort | Offline | Branding | Best for |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **1** | **Link out** | Host calls `CreateAttempt`, gets a URL, sends the candidate | ~10 min | **Full PWA** | Co-brand | Email invites, pilots, first integration |
| **2** | **Embed** | `<assessmatrix-runner>` web component → sandboxed iframe + postMessage bridge | ~1 hour | Degraded | Host theme tokens | Assessment *inside* an ATS/LMS screen |
| **3** | **Headless** | Connect RPC + SDK; host renders its own UI from our item/delivery API | 1–2 weeks | Host's own | Fully host | Deep product integration, OEM |
| **4** | **Sovereign** | Self-host, or our cloud on a CNAME'd customer subdomain | Deployment | **Full PWA** | Fully host | Enterprise, LSP, government, data residency |

**Door 2 is the default we market. Door 1 is the on-ramp. Door 4 is the enterprise close.**

### Why a web component wrapping an iframe, not a React component

We could ship a React component — our own client is React 19 — but a `<script>` + custom
element works in React, Vue, Angular, Svelte, Rails, Laravel, WordPress and plain HTML with
one artifact. More importantly, **the iframe is a security and integrity boundary**: the
host page cannot read the item content, cannot script the runtime, and cannot tamper with
the attempt event log. For high-stakes assessment that boundary is the product. A bare React
component would run inside the host's JS context, where anything is possible.

The loader script stays tiny (target <6 KB gzipped) and immutably cached — it will sit on
customer sites we don't control, so it must be boring.

## 3. Embed security model

- **Never a long-lived key in the browser.** The host's backend uses its API key to mint a
  short-lived **attempt token** (JWT: audience = the host origin, subject = the attempt,
  scope = exactly one attempt, expiry in minutes). That token is all the browser ever sees.
- **Per-tenant origin allowlist** enforced in `Content-Security-Policy: frame-ancestors`.
- **postMessage handshake** with an explicit `targetOrigin` both ways — never `"*"`.
- **iframe `sandbox`** with the minimum grants, plus the Permissions Policy delegation
  proctoring needs.

### The two gotchas that silently break embedded assessment

These are worth building product around, not just documenting:

1. **Camera/mic delegation.** A cross-origin iframe gets no camera unless the host adds
   `allow="camera; microphone"`. Without it, Tier 2+ proctoring fails *silently* — the exam
   runs and the integrity signal is simply absent. Unacceptable for a high-stakes product.
2. **Third-party storage partitioning.** Modern browsers partition storage for embedded
   third-party origins, so the offline queue and service worker cannot behave like a
   first-party PWA. **Door 2 therefore cannot promise full offline.**

Both get surfaced in a **live Preflight panel** in the embed configurator (the Embed
studio screen in `apps/studio`) that tells the integrator what will break *before* they ship, and names
the fix. Offline-critical customers get routed to Door 1 or Door 4, where the runtime is
first-party on its own origin and the service worker has full power.

Being honest about this in the UI is a differentiator. Competitors let it fail in production.

## 4. Events: the integration contract

Because **attempts are event-sourced** (`03-architecture.md`), the outbound integration
comes almost free — and gets a capability nobody else offers.

**Event catalogue (draft):**

| Domain | Events |
| :--- | :--- |
| Attempt | `created` · `started` · `resumed` · `submitted` · `expired` · `scored` |
| Integrity | `flag.raised` · `flag.cleared` · `review.queued` · `review.completed` |
| Identity | `verification.passed` · `verification.failed` |
| Credential | `issued` · `revoked` · `verified` |
| Content | `item.published` · `item.retired` · `assessment.published` |
| Skill graph | `evidence.recorded` · `profile.updated` |

**Delivery rules:** HMAC-signed with a timestamp, at-least-once, **strictly ordered per
attempt**, idempotency key on every payload, exponential backoff with a dead-letter view.

**Shipped (the Webhooks screen, `WebhookService`, `services/core/internal/webhook`):**

| Event | When |
| :--- | :--- |
| `exam.published` | A version of an instrument is published |
| `assignment.invited` | A link is mailed: first send, resend, or the candidate asked for a new one |
| `attempt.started` | A candidate starts (resuming is not a second start) |
| `attempt.submitted` · `attempt.expired` | Handed in, or closed by the server's clock; carries the score and each question's points |
| `attempt.scored` | A person scored an essay; carries the new score and each question's points |
| `credential.issued` · `credential.revoked` | Open Badges credential issued, restored or revoked |
| `webhook.ping` | "Send test event" on the screen |

- Each request is `POST` JSON `{"id", "type", "created_at", "data"}`. `data` names records by id
  (`attempt_id`, `assignment_id`, `exam_id`, `person_id`) with versions, times and scores;
  never a name, email, answer or live link.
- An attempt's `submitted`, `expired` and `scored` events carry `score` (`pct`, `points`,
  `max`, `passed`, `pending_review`) and `items`: one `{"item_id", "points", "max"}` per scored
  question, in the instrument's order, where `max` is what the author set the question to be
  worth and `points` is `null` while an essay waits for a person. Scale questions are left out.
  In the rare case the list would push the body past 64 KB it is dropped and
  `"items_omitted": true` is sent instead.
- `AssessMatrix-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>`. Reject a
  `t` more than five minutes old. `webhook.Verify` is the reference implementation.
- `AssessMatrix-Event-Id` is the same on every retry and redelivery: de-duplicate on it.
- The event is written in the same transaction as the change, so a rolled-back change sends
  nothing. Delivery is at least once.
- **Ordered per attempt.** An attempt's events and its credential's reach each endpoint in the
  order they happened: a later one waits while an earlier one is still being retried, and
  goes once that is delivered or out of tries. Events about different attempts, and the rest,
  are not ordered against each other: order those by `created_at`.
- Ten tries over about four hours, then the delivery is marked failed and can be sent again
  from the screen. The log is kept 30 days; erasing a person deletes the events about them.
- Receivers must be `https` on a public address. Private, loopback, link-local and metadata
  addresses are refused when the URL is saved and again at connect time, after DNS;
  redirects are not followed. `AM_WEBHOOK_ALLOW_PRIVATE=1` (with `AM_DEV=1`) lifts this for a
  receiver on a developer's machine.
- **Replay** ("Replay events" on the screen, `ReplayEvents`) sends one endpoint every logged
  event about one attempt, or every event in a time window, again, in order, with the original
  event ids and bodies. Only types the endpoint subscribes to are sent, including events from
  before it subscribed; one already waiting to send is left as it is. Up to 1,000 events per
  replay, within the 30-day log.

**The differentiator — replay.** Since the log is the source of truth, a customer can replay
any webhook window: "resend everything for attempt X," or "resend all events from 14:00 to
16:00 yesterday." Integration debugging stops being a support ticket. Ship a self-serve
replay UI (the Event inspector screen in `apps/studio`) from day one.

## 5. Standards doors (orthogonal to the four)

Per `01-market-and-competition.md`, standards are the anti-lock-in sales weapon. Each is an
adapter over the same core, never a fork of it.

| Standard | Direction | Surface | Phase |
| :--- | :--- | :--- | :--- |
| **QTI 3.0** | in/out | Item bank import/export | MVP |
| **Open Badges 3.0 / W3C VC** | out | Credential issuance to a wallet the person owns | MVP-adjacent |
| **LTI 1.3** + Deep Linking + AGS | in | Launch from Moodle/Canvas; grades flow back | Campus |
| **OIDC / SAML** | in | SSO | Early (enterprise gate) |
| **SCIM 2.0** | in | User/roster provisioning | Campus/Enterprise |
| **xAPI / Caliper** | out | Analytics egress to the customer's warehouse | Later |
| **ATS marketplaces** | both | Greenhouse, Workable, Ashby listings | Hire |

## 6. Reverse integration: Matrix Extensions

The doors above are *us inside them*. The platform play is **them inside us** — and it's how
the four-segment engine scales without us authoring every vertical ourselves.

Three extension points, each a narrow contract:

- **Custom item types** — a sandboxed renderer + response schema + scorer. Lets a nursing
  board ship a clinical-simulation item, or a university ship a chemistry equation editor,
  without us building it.
- **Custom graders** — receive a response payload, return a score plus rationale. Runs in the
  same isolated runner as candidate code (`03-architecture.md` §4), with the same
  egress-denied posture.
- **Integrity signal providers** — third-party device attestation, ID verification vendors,
  or a regional biometric service, emitting into the same attempt event log as first-party
  signals.

Extensions are **tenant-scoped by default, publishable to a directory when certified**. This
is also the clean answer to "can you support our weird exam format?" — yes, and you can
build it yourself.

## 7. What each surface integrates with

| Surface | Primary door | Key integrations |
| :--- | :--- | :--- |
| **Certify** | 4 Sovereign | Membership/registration systems, payment, credential wallets, OB3 |
| **Hire** | 2 Embed + marketplaces | Greenhouse, Workable, Ashby, Lever, iCIMS, local ID ATS |
| **Campus** | LTI 1.3 | Moodle, Canvas, campus SIS via SCIM |
| **Grow** | 3 Headless | HRIS, LMS, skills/competency frameworks |

## 8. Open questions for the brainstorm

1. Do we expose the **skill graph** as a public read API early? It's the most valuable and
   most copyable asset. Leaning: expose *per-person evidence* (the person owns it) but not
   *aggregate calibration* (that's the moat).
2. Do partner SDKs ship hand-written ergonomics, or raw generated stubs? Generated is free
   but reads badly; a thin hand-written layer over generated stubs is the usual compromise.
3. Is there a free "verify this credential" endpoint with no auth? Probably yes — verification
   ubiquity is what makes issued credentials worth having.
4. REST gateway: build it, or refuse? Connect already speaks JSON over HTTP/1.1, which covers
   most "we need REST" objections. Leaning: no separate gateway; document the JSON mode.
