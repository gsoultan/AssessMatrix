# Product Concept

## 1. The problem with "all four segments"

You asked for hiring + education + certification + corporate. Stated plainly: serving four
buyers at once is the standard way to build a product that wins none of them. The buyers,
compliance regimes, sales motions and success metrics barely overlap.

But there is a version of this that is *stronger* than picking one — and it's the reason the
name AssessMatrix is right. It requires two disciplines:

1. **One engine, four thin surfaces.** ~80% of the system is identical across segments.
   Build that once, verticalize only the surface.
2. **Sequence the go-to-market.** Build all four *architecturally* from day one; sell to
   exactly one at a time.

### What actually differs

| | Hire | Campus | Certify | Grow |
| :--- | :--- | :--- | :--- | :--- |
| Buyer | TA / HR lead | Faculty, kurikulum | Certification body, LSP | L&D |
| Stakes | Medium | Medium | **Very high** | Low |
| Identity rigor | Moderate | Low–moderate | **Biometric + chain of custody** | None |
| Item source | Vendor library | Teacher-authored | SME committee + psychometrician | Internal SME |
| Integration | ATS | LMS via LTI 1.3 | Registration + credential systems | LMS / HRIS |
| Output | Hire signal | Grade | Pass/fail credential | Skill gap |
| Willingness to pay | High | **Low** | **Highest** | Low |

### What is identical (the engine)

Item bank · item types · authoring · delivery runtime · autosave/resume · auto-grading ·
code execution · integrity tiering · proctoring · psychometrics · analytics · anti-AI task
design · multi-tenancy · i18n · billing · audit trail · accessibility.

## 2. The concept: a skills graph, not a test catalog

Every incumbent sells **tests**. AssessMatrix sells **verified skill evidence**.

The Matrix is `skill × evidence × context`:

- Every item is tagged to a node in a **skills taxonomy**, with difficulty and
  discrimination parameters.
- Every attempt emits **evidence** against those nodes, with a confidence interval — not a
  single opaque score.
- Evidence accumulates **across contexts**. A student's campus exam, their professional
  certification, their hiring screen and their workplace review all write to the same
  person-level graph, and all read from the same calibrated item pool.
- Verified achievements issue as **Open Badges 3.0 / W3C Verifiable Credentials** into the
  person's own wallet — portable, cryptographically verifiable, no vendor lock-in.

**This is the answer to "why four segments":** a single-segment incumbent can never build
this graph. It is a cross-segment artifact by definition, and the four segments feed each
other — universities feed certification bodies feed employers. That is a network effect none
of TestGorilla, HackerRank, ExamSoft or Talview can reach from where they stand.

## 3. Three layers + four surfaces

### Layer 1 — Item Engine
- **QTI 3.0 native.** Import and export the whole bank. Never hold content hostage.
- **Parameterized generative items.** An item is a *template* with constrained variable
  slots and a solver, not a fixed string. Each candidate gets a unique instantiation drawn
  from the same difficulty band. **There is no static bank to leak**, which structurally
  defeats question dumps — the failure mode that eventually destroys every item bank.
- **Calibration.** Classical test theory (p-value, point-biserial, distractor analysis) from
  day one; IRT / 2PL and CAT once volume exists. Be transparent about which is in force.
- Versioned, reviewable, with an SME workflow (draft → review → pilot → live → retired).

### Layer 2 — Delivery & Integrity
- Adaptive delivery: linear, section-adaptive (MSAT), and full CAT.
- **Offline-capable candidate runtime.** Service worker + encrypted local queue, sync on
  reconnect. Non-negotiable for Indonesian schools and a genuine gap vs. most US vendors.
- **Integrity as a tier, not a switch:**

  | Tier | Name | Controls | Cost to run |
  | :--- | :--- | :--- | :--- |
  | 0 | Open | None — practice, formative | ~0 |
  | 1 | Monitored | Device fingerprint, focus/blur, timing anomalies, unique items | Low |
  | 2 | Proctored | + on-device vision (face presence, gaze, second person), event evidence, lockdown | Medium |
  | 3 | Certified | + biometric identity match, keyframe retention, human review queue, chain of custody | High |

- **On-device inference first.** Run vision models in the browser (WebRTC + ONNX/MediaPipe);
  upload *events and hashed keyframes*, not continuous video, unless Tier 3 requires it.
  This is simultaneously the bandwidth advantage, the cost advantage, and the privacy
  advantage — and continuous video upload is exactly what makes Proctorio and Honorlock
  expensive and disliked.
- **Process evidence over answer evidence.** The attempt event log — every keystroke batch,
  paste, revision, run, failure and recovery — is the primary integrity signal. A pasted LLM
  answer has no process. This does not require watching the candidate's face.
- **AI-in-the-loop tasks.** For technical and analytical roles, hand the candidate a scoped
  AI assistant inside the task and grade the *collaboration*: do they verify claims, catch
  the wrong answer, direct it usefully, know when to stop. Karat proved the design works with
  a human in every seat; the opportunity is to do it automatically.

### Layer 3 — Evidence & Credential
- Attempt → skill-node evidence with confidence intervals.
- Score reports with item analysis, cohort norms, and **explainability** (which evidence
  drove which conclusion) — required for EU AI Act, NYC LL144, and Indonesia's expected 2026
  AI rules.
- Built-in **bias/adverse-impact auditing** per assessment, per demographic slice, as a
  standing report — not a consulting engagement.
- **Open Badges 3.0 issuance** to a wallet the person owns.

### Purpose is a field, not a product

An instrument is an *exam*, a *quiz* or a *questionnaire*. That one field decides whether
answers have a key, whether a clock runs, and whether results are a score or a distribution —
and nothing else changes. The same delivery engine that runs a Tier 3 proctored certification
runs an anonymous onboarding survey.

This matters commercially: **Grow** and **Campus** buyers want questionnaires (engagement
surveys, course feedback, training evaluation) far more often than they want proctored exams,
and it is the cheapest possible way into an account that later buys the graded product.

### The four surfaces
Thin verticalized apps over the same core — different vocabulary, workflows, integrations
and reports; identical engine.

- **Matrix Hire** — ATS-integrated, role batteries, AI-in-the-loop technical tasks.
- **Matrix Campus** — LTI 1.3 into Moodle/Canvas, offline-first, cohort analytics.
- **Matrix Certify** — item committees, psychometric review, Tier-3 proctoring, credential issuance.
- **Matrix Grow** — skills-gap mapping against the same graph, low-stakes, HRIS-integrated.

## 4. Pricing — attack the documented complaint

TestGorilla's three loudest G2 complaints are *expensive for infrequent use*, *annual
lock-in and inflexible cancellation*, and *rigid contracts*. HackerRank has no free tier at
all. TestGorilla's credit math (1 invite = 1 credit, full evaluation = 3) is confusing by
design. That is a soft target.

**Price by integrity tier, not by mystery credits.** Integrity tier is the thing that
actually drives your cost — compute, storage, human review — so it's an honest axis, and
nobody in the market prices this way.

| Plan | Shape |
| :--- | :--- |
| Free | Unlimited Tier 0/1 attempts, full authoring, 25 Tier-2 attempts/mo |
| Growth | Monthly, **no annual lock-in**, transparent per-attempt rate by tier |
| Certify | Per-exam-event pricing, Tier 3, SLA, human review included |
| Self-host / Sovereign | Annual license; **data residency is the feature**, for LSPs, universities, government |

IDR-native pricing for Indonesia, USD for global. Publish the price list. "Export your
entire item bank as QTI 3.0 at any time" is the closing line.

## 5. Moats, ranked by durability

1. **Calibrated item graph** — compounds with every attempt; cannot be bought.
2. **Credential network effects** — issuers + verifiers on the same rail.
3. **PDP / data-residency compliance in Indonesia** — a regulatory moat US vendors won't cross.
4. **AI-in-the-loop + process-evidence scoring IP** — the post-detection paradigm.
5. **Standards-native, anti-lock-in positioning** — a sales weapon more than a moat, but it
   wins deals against every incumbent except TAO.
