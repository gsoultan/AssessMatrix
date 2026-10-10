# Market & Competitive Landscape

Research date: 2026-09-21. All figures sourced; links at the bottom.

## 1. Market size

| Market | 2025/26 | Forecast | CAGR |
| :--- | :--- | :--- | :--- |
| Online assessment software | $4.55B (2025) → $5.31B (2026) | $9.03B by 2030 | 16.7% |
| Talent assessment services | $9.88B (2025) → $10.70B (2026) | $15.1–18.1B by 2030–32 | 8.7–9.1% |
| Indonesia edtech | $3.2B | $8.81B by 2033 | 11.8% |

Adoption tailwind: **70% of employers now use some form of skills-based hiring**, up from
58% in 2023; 65% use it for entry-level, and 90% of those assess during the interview stage.

## 2. The structural break: AI has broken the incumbent product

This is the single most important finding of this research.

| Signal | 2026 figure |
| :--- | :--- |
| Technical candidates flagged for AI assistance (19,368 live interviews) | **48%** |
| Flagged cheaters who would still advance past the hiring bar | **61%** |
| Proctored coding-assessment fraud, year over year | **16% → 35%** |
| Estimated true fraud rate, asynchronous unproctored technical assessments | **60–80%** |
| Score inflation, unproctored vs matched proctored cohorts | **>4x larger** |

Incumbent responses are all *detection*, and all reactive:

- **HackerRank** shipped Gaze Detection in July 2026 (flags looking away then resuming
  typing) plus upgraded screenshot analysis in Proctor Mode.
- **CodeSignal** assigns a proprietary Suspicion Score; among flagged 2025 assessments,
  35% showed off-screen referencing and 23% showed unusually linear typing with minimal
  pauses or debugging.

Meanwhile a whole counter-industry openly markets "invisible AI help for HackerRank /
CodeSignal / CodeSubmit". Each detector only sees its own page; Zoom, Meet, Teams and
Chime run **zero** AI-cheating detection on a shared screen.

**Read:** the arms race is unwinnable and the incumbents know it. The only durable answers
are (a) unique per-candidate items, (b) process evidence, (c) AI-in-the-loop task design.
Karat's NextGen format already moved here — a live engineer watches the candidate work in a
real multi-file project *with* an AI assistant, grading how they validate AI-generated code
and weigh tradeoffs. It works, but it needs a human per interview, so it can't scale down.
**That gap — AI-in-the-loop rigor without a human in every seat — is the opening.**

## 3. Competitive comparison

### Hiring / technical
| Vendor | Public pricing | Strength | Exploitable weakness |
| :--- | :--- | :--- | :--- |
| **TestGorilla** | from ~$142–499/mo, **annual only**; credit system where 1 invite = 1 credit but a full evaluation = 3 | Huge library, easy UX | G2 complaints are a roadmap: *too expensive for small/infrequent use* (45 mentions), *unclear contracts & inflexible cancellation* (33), *rigid pricing, locked contracts, limited free tests* (28) |
| **HackerRank** | from $165 / 10 attempts / mo, **no free option** | Engineer brand, AI plagiarism detection, code-review scoring | Expensive from the first dollar; public question dumps; detection-led strategy |
| **Codility** | Quote only | Enterprise technical depth | Opaque pricing, enterprise-only motion, no self-serve |
| **CodeSignal** | Quote | Suspicion Score, proctored exam framework | Same arms race; heavy |
| **iMocha** | ~$400/mo annual, quote-gated | 3,000+ skills incl. non-tech | Quote gate kills SME self-serve |
| **Karat (NextGen)** | Service, high ACV | AI-in-the-loop interview design — the right idea | Human interviewer per session; cannot scale down or serve SMEs |
| **micro1 ("Zara")** | Service | Scalable AI interviews | AI-mediated assessment criterion validity measured at **.24** — below top-tier human structured interviews |

### Certification / high-stakes
| Vendor | Strength | Weakness |
| :--- | :--- | :--- |
| **Mercer Mettl** | End-to-end exams + proctoring, big in India/SEA | G2: weaker UX than Talview despite solid capability |
| **Talview** | 120+ countries, multi-layer AI integrity, behavioural analysis | Enterprise-only, expensive |
| **ExamSoft** (Turnitin) | **Offline exam delivery**, deep analytics, high-stakes credibility | No trial, quote-only, legacy UX |
| **Questionmark** | Item banking, authoring, analytics, proctoring integration | Legacy platform |

### Education / proctoring
| Vendor | Strength | Weakness |
| :--- | :--- | :--- |
| **Proctorio** | Lowest bandwidth in the industry, E2E encryption | Privacy backlash |
| **Honorlock** | AI + on-demand live proctor pop-in, Chrome extension | Institutions actively seek alternatives over *privacy intrusion, high cost, inflexible monitoring* |
| **TAO** (Open Assessment Technologies) | First commercial-grade open-source assessment platform; **QTI + LTI + WCAG native** | Ops burden, dated UX, not hiring-oriented |
| **Moodle / Canvas quizzes** | Already installed, zero acquisition cost | Weak item banking, weak integrity, single-segment |

### Indonesia
| Vendor | Position | Gap |
| :--- | :--- | :--- |
| **Talentics** | Claims #1 online assessment + recruitment in Indonesia | Recruitment-only |
| **NSD (Nirmala Satya)** | 1,000+ companies, psikotes online; claims 75% time / 60% cost saving | Psychometric-only, service-led |
| **Taldio**, **Talenta Indonesia**, biro psikologi | Psychologist-designed batteries | Service bureau, not product; no technical assessment, no certification, no education |

**The Indonesian market is service-bureau shaped, psikotes-centric, and has no product-led
cross-segment player.** Meanwhile ANBK — the national assessment — already runs on
**computerized multistage adaptive testing (MSAT)**, so adaptive testing is policy-normalized
and school buyers already understand the concept. That is an unusually good tailwind.

## 4. Standards = the anti-lock-in weapon

- **QTI 3.0** — portable items. Interoperability standards explicitly *prevent vendor lock-in
  and preserve ownership of assessment content*. Every incumbent above except TAO benefits
  from lock-in; saying "export your whole bank any time" is a sales weapon, not a feature.
- **LTI 1.3** — launch a test from inside Moodle/Canvas so seamlessly the student never
  knows another system delivered it. This is the education distribution channel.
- **Open Badges 3.0 / W3C Verifiable Credentials** — final standard June 2024; mass
  institutional adoption running through 2025–2026 (Credly, Canvas Credentials, Accredible,
  POK certifying as issuers). Badges live in W3C VC wallets, exportable to Apple/Google Wallet.
  Aligned with **CLR 2.0**. This is the rail that makes evidence portable across all four segments.

## 5. Distribution

- **Hiring:** Greenhouse's marketplace carries 500+ integrations and already lists Codility,
  HackerRank, Pymetrics. Workday / Greenhouse / Lever / iCIMS each need different integration
  architectures. A marketplace listing is a sales asset, not just plumbing.
- **Education:** LTI 1.3 is the equivalent channel.
- **Certification:** direct, relationship-led, but stickiest once accredited.

## 6. Regulatory

- **Indonesia UU PDP No. 27/2022** — transition ended 17 Oct 2024, **fully in force now**.
  Biometrics are *sensitive personal data*: processing triggers a **DPIA** and a **DPO**.
  The PDP Agency is targeted to be operational in 2026, and **mandatory AI regulation is
  expected early 2026** — directly relevant to automated proctoring and AI scoring.
- Implication: face-matching + video proctoring is sensitive biometric processing. US vendors
  store this offshore. **Indonesian data residency + PDP-native consent flows is a moat no US
  incumbent will bother to build.**
- Global later: EU AI Act puts employment-related AI in the high-risk category; NYC LL144
  requires bias audits. Build bias auditing in from day one, not bolted on.

## Sources

- https://www.researchandmarkets.com/reports/5970969/online-assessment-software-market-report
- https://www.360iresearch.com/library/intelligence/talent-assessment-services
- https://www.openpr.com/news/4359428/indonesia-edtech-market-to-reach-usd-8-811-52-million-by-2033
- https://aiseptor.com/research/ai-cheating-statistics-2026
- https://www.herohunt.ai/blog/detecting-ai-interview-cheating-2026/
- https://www.hackerrank.com/writing/best-ai-powered-technical-screening-tools-2025-feature-comparison
- https://www.aceround.app/blog/karat-interview-ai-guide/
- https://www.micro1.ai/ai-interview-guide
- https://onlinelibrary.wiley.com/doi/10.1111/ijsa.70056
- https://www.willo.video/blog/testgorilla-pricing-review
- https://www.g2.com/products/testgorilla/reviews?page=10&qs=pros-and-cons
- https://blog.imocha.io/testgorilla-vs-hackerrank
- https://www.g2.com/compare/mercer-mettl-online-examination-and-proctoring-solutions-vs-talview
- https://integrityadvocate.com/resource-center/blog/best-online-proctoring-tools-in-2026/
- https://testnhire.com/blog/honorlock-alternatives-for-online-proctoring
- https://www.taotesting.com/blog/oat-launches-tao-community-edition/
- https://www.1edtech.org/standards/qti
- https://www.1edtech.org/standards/open-badges
- https://unified.to/blog/6_assessment_apis_to_integrate_with_in_2026_greenhouse_workable_ashby
- https://practiceguides.chambers.com/practice-guides/data-protection-privacy-2026/indonesia/trends-and-developments
- https://www.talenta.co/en/blog/hr-uu-pdp-compliance-roadmap/
- https://www.talentics.id/
- https://nsd.id/psikotes-online
- https://unesdoc.unesco.org/ark:/48223/pf0000387827
