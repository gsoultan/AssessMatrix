# Developer Profile Panel — AssessMatrix

Non-trivial changes are worked as a pair. Adopt the **Driver** profile that owns the code you
touch, then re-read your own diff as the **Challenger** — the profile whose budget your change
most likely breaks — and answer its vetoes honestly. Name both in the task summary
(`Driver: delivery · Challenger: integrity`).

| Profile | Owns | Vetoes | Proof required |
| :--- | :--- | :--- | :--- |
| **integrity** | Attempt event log, integrity tiers, proctoring signals, flag explainability | Any change that weakens the audit trail, makes a flag unexplainable, or lets a fast path skip an integrity check | Replay of a tampered attempt still detected; every new flag type renders a plain-language reason |
| **psychometrics** | Item calibration, scoring, CAT selection, bias reporting | Any scoring change without an item-analysis impact report; any claim of calibration not backed by data | Score distribution diff on a reference cohort; adverse-impact ratio unchanged or improved |
| **sec** | Sandbox isolation, tenancy boundaries, credential handling, PII segregation | Any code execution change that widens the blast radius; any query that can cross `tenant_id`; any PII crossing a region boundary | Tenant-isolation test passes; runner node has no egress and no credentials; region assertion in the storage client |
| **privacy** | Consent records, retention, residency, erasure | Any new personal-data field without a purpose, a retention rule and a lawful basis; any biometric capture without DPIA coverage | Retention job deletes it; consent row exists; DPIA section updated |
| **delivery** | Candidate runtime, offline queue, resume, accessibility | Any change that loses candidate work, breaks offline sync, or regresses WCAG 2.2 AA | Kill-the-browser and network-partition tests pass; axe scan clean |
| **perf/cost** | Bandwidth per attempt, media egress, microVM minutes, DB load | Any per-attempt cost regression; continuous media upload below Tier 3 | Cost-per-attempt delta reported on the diff |

## Standing truths

- A fast path that skips a check is a vulnerability.
- A check that allocates per request is a regression.
- A cache that ignores who asked is a data leak.
- Any map keyed by attacker-supplied input needs a bound and an eviction.
- **Losing a candidate's in-progress attempt is the worst bug this system can have.** It is
  worse than downtime: the candidate cannot re-sit a high-stakes exam.
- Every bug fix ships a test that fails before and passes after, with the root cause named in
  one sentence.
