# Roadmap

## 1. Go-to-market sequencing — and why

Build for four segments. **Sell to one at a time**, in this order:

### Phase 1 (months 0–6) — Certification & professional bodies, Indonesia
**Recommended beachhead.** Reasoning:

- **Highest willingness to pay per seat.** High stakes means integrity is a must-have, not a
  nice-to-have, and the budget already exists.
- **Underserved locally.** Indonesia's assessment vendors are psikotes service bureaus
  (Talentics, NSD, Taldio). None offer item banking + psychometrics + Tier-3 proctoring +
  credential issuance as a product.
- **The compliance moat is real here and nowhere else.** UU PDP data residency for biometric
  proctoring is a requirement US incumbents won't build for. BNSP/LSP and government-adjacent
  buyers will treat it as disqualifying if absent.
- **Build the hardest segment first.** Identity, chain of custody, psychometric defensibility,
  audit trail and appeals are a *superset* of what hiring, campus and L&D need. Scaling **down**
  from high-stakes is trivial; scaling **up** from a quiz tool is impossible — that's the
  trap that keeps TestGorilla out of certification.
- **Seeds the graph with trusted evidence.** Certification bodies are the natural first
  credential issuers, so the skills graph starts with the most credible nodes.

Target: 2–3 design partners, 1 paid production exam event.

### Phase 2 (months 6–12) — Hiring, self-serve
The product-led acquisition surface. AI-in-the-loop technical tasks + process-evidence
scoring, priced transparently, monthly, no annual lock-in — aimed straight at TestGorilla's
three loudest complaints. IDR pricing for Indonesian SMEs who cannot justify $499/mo.
Integrations: Greenhouse, Workable, Ashby marketplaces + local ATS.

### Phase 3 (months 12–18) — Campus
LTI 1.3 into Moodle/Canvas and campus LMSs. Offline-first runtime is the wedge — the thing
most US vendors handle badly and Indonesian institutions need daily. Adaptive delivery is
already policy-normalized via ANBK's MSAT, so the concept needs no education. Low margin,
high volume, high strategic value: universities feed certification bodies feed employers,
and that loop is the moat.

### Phase 4 (months 18–24) — Grow + global English-first
Skills-gap mapping on the existing graph. Global launch positioned on **AI-immune assessment**,
which is the one axis where every incumbent is visibly losing.

## 2. MVP — 12 weeks

Scope discipline: the MVP proves the **engine**, on **one real certification exam**, with a
**design partner**. Nothing else ships.

| Weeks | Milestone | Done when |
| :--- | :--- | :--- |
| 1–2 | **Foundations** — Go core skeleton, Postgres + RLS multi-tenancy, auth, audit log, **event-sourced attempt model** | A CI test proves tenant A cannot read tenant B's rows through any endpoint |
| 3–5 | **Item Engine v1** — QTI 3.0 subset (choice, text entry, ordering, match, extended text), parameterized generative items, bank CRUD, versioning, SME review workflow | A real QTI 3.0 file round-trips import → edit → export without loss |
| 6–8 | **Delivery runtime** — offline-capable candidate app, linear + section-adaptive delivery, autosave/resume, scoring, WCAG 2.2 AA | An attempt survives a browser kill and a 5-minute network outage with zero data loss |
| 9–10 | **Integrity tiers 0–2** — lockdown, device fingerprint, on-device vision events, focus/paste/timing signals, explainable flag timeline | Every flag renders a plain-language reason a candidate could contest |
| 11–12 | **Results & credentials** — score reports, CTT item analysis (p-value, point-biserial, distractor analysis), Open Badges 3.0 issuance, **design-partner pilot exam run live** | Real candidates sat a real exam; the partner accepted the results |

**Explicitly deferred past MVP:** code execution, AI-in-the-loop tasks, IRT/CAT, Tier 3
biometrics, ATS/LTI integrations, billing, the other three surfaces.

## 3. Quarters after MVP

- **Q2** — Code execution (Tier A) · IRT calibration + CAT · Tier 3 biometrics + human review
  queue · billing · first paid certification customers.
- **Q3** — AI-in-the-loop task type (Tier B microVM sessions) · process-evidence scorer ·
  Hire surface + ATS integrations · self-serve signup and public pricing.
- **Q4** — LTI 1.3 + Campus surface · bias/adverse-impact reporting · self-host packaging
  hardened for sovereign deals.

## 4. Risk register

| # | Risk | Severity | Mitigation |
| :--- | :--- | :--- | :--- |
| 1 | **Four-segment focus dilution** | High | One surface sold at a time; engine-vs-surface split enforced in code review. If a feature only serves one surface, it may not enter core |
| 2 | **Psychometric credibility gap** | High | Certification buyers will reject an un-credentialed vendor. Hire or retain a psychometrician before the first Certify deal. Publish methodology. Use CTT honestly until IRT data volume exists — do not imply calibration you don't have |
| 3 | **Sandbox escape** | Critical | Disposable, egress-denied, credential-free runner nodes. Assume escape; make it worthless. External pentest before code execution reaches GA |
| 4 | **Proctoring privacy backlash** | High | On-device inference, events-not-video by default, bounded retention, candidate-visible evidence, published DPIA. The backlash against Proctorio/Honorlock is a documented market opening — don't reproduce their design |
| 5 | **AI-scoring regulation** (EU AI Act, NYC LL144, Indonesia AI rules expected early 2026) | Medium-High | Explainability and bias auditing in the data model from day one; never ship a flag that can't state its reason |
| 6 | **Item cold start** | Medium | Generative parameterized items reduce the bank size needed. Start with CTT + borrowed norms. Partner content for breadth rather than authoring 3,000 skills alone |
| 7 | **ATS/LMS bundling squeezes standalone assessment** | Medium | Be the layer they integrate, not the one they replace: standards-native, marketplace-listed, and differentiated on integrity where bundled quiz modules are weakest |
| 8 | **Cheap-infra promise vs. microVM costs** | Medium | Tier B is metered and priced at Tier 2/3 only; never on free. Self-host bundle ships without Tier B by default |

## 5. What would make me change this plan

- A certification design partner doesn't materialize in 8 weeks → flip Phase 1 and Phase 2,
  lead with self-serve hiring and build high-stakes capability against inbound demand.
- A well-funded competitor ships credible AI-in-the-loop assessment at SME pricing → move
  the differentiation to the credential graph and data residency, and compete on distribution.
