# 10 · DPIA draft: integrity signals and the on-device camera check

Status: **draft for the DPO's review**, written before the camera signal ships (docs/09, privacy
track; `AGENTS.md`, privacy reviewer). It covers tiers 1 and 2 as built in Phase 5. Tier 3
(identity match, keyframes) is not built and needs its own assessment before it is.

## 1. What is processed, and why

| Purpose (consent key) | Tier | What is recorded | What is never recorded |
| :--- | :--- | :--- | :--- |
| `signals` | 1+ | Time the exam was out of view and for how long; number of characters pasted into an answer; time on each question; a one-way hash of the device's browser traits and its class (phone/tablet/desktop); a refused second tab | The pasted text; what was on other tabs or apps; keystrokes; the device's identity in the clear |
| `fullscreen` | 2+ | Each exit from full screen | Anything shown on screen |
| `camera` | 2+ | Stretches with no face in view (duration), with more than one face (duration, most faces seen), and that the check stopped (reason) | Video, frames, photos, face embeddings, landmarks, identity |

The purpose is the same for all three: to give a human reviewer specific, checkable reasons to
look at an attempt before its result is relied on. No signal decides a result on its own: a
flag asks for review, it never fails a candidate.

**Lawful basis:** explicit consent, per purpose (UU PDP No. 27/2022 art. 20(2)(a), 22). The
organiser picks the tier; the runner lists each purpose the tier needs, separately, in
Indonesian, before the start button (`apps/studio/src/integrity.ts`, `CandidateApp.tsx`
consent stage). Declining is possible and says what happens: the exam cannot be taken on this
tier, nothing is recorded, and the candidate is pointed to the organiser for another route.

## 2. Is the camera check biometric processing?

The detector (MediaPipe BlazeFace short-range) answers "is there a face here, and how many".
It does not compute an embedding, does not compare faces, and cannot say *whose* face it is.
That is processing of images of a person (personal data), but not biometric data used to
identify someone. We still treat it with the care of specific personal data, because a camera
in a candidate's home is intrusive whatever the output, and because Tier 3 will be biometric.

## 3. Data flow

1. The candidate's browser opens the front camera at 320×240 after consent.
2. About once a second a frame is copied to an `ImageBitmap` and handed to a worker in the same
   tab, which runs the detector and returns a face count. The bitmap is closed straight away.
   Nothing is drawn, stored or sent. The video element is invisible and detached on stop.
3. The runner turns counts into events (`vision.face_missing`, `vision.faces_multiple`,
   `vision.stopped`) with durations as numbers, and queues them with the answers.
4. The core stores them in the attempt's hash-chained log, in the tenant's region, under row
   level security. It drops (does not store) any signal whose consent row is missing, so a
   modified client cannot collect beyond what the candidate agreed to.
5. Rules (`services/core/internal/domain/integrity.go`) derive flags; each flag carries the
   sentence a candidate can check, in English for staff and Indonesian for the candidate.

The detector's runtime and model are served from our own origin, never a CDN, so no third
party learns who is sitting an exam. They are fetched only by a tier 2 attempt.

## 4. Risks and how they are treated

| Risk | Likelihood | Severity | Treatment |
| :--- | :--- | :--- | :--- |
| Video or images leave the device | Low | High | No code path uploads media; the runner only sends numbers. The service worker never caches RPCs. Review gate: any change to `vision.ts` / `vision.worker.ts` goes through the privacy reviewer |
| False flags (poor light, darker skin tones, glasses, a looking-down habit) | Medium | Medium | Thresholds are long (30 s total, 15 s in one stretch for a missing face); flags ask for review, never fail; the reason names the durations so the candidate can contest them. To do before the pilot: measure the detector's miss rate on a consented, diverse internal sample and record it here |
| Household members captured | Medium | Low | Only a count of faces is kept; no image of anyone is kept |
| Collection beyond consent by a modified client | Low | Medium | Server drops unconsented signals (tested: `TestNoSignalWithoutItsConsent`) |
| Over-retention | Medium | Medium | Attempt records, signals included, are deleted at most one year after the exam by an hourly job, with an audit record (`internal/api/retention.go`, tested: `TestRetentionDeletesTheRecordAndKeepsTheScore`). A tenant may shorten the period, never lengthen it past the year the consent text promises (database check) |
| Pressure to consent | Medium | Medium | The organiser, not us, decides the tier; the consent text says the alternative is to contact the organiser. Organisers must offer a non-camera route on request (to put in the customer terms) |
| Candidate cannot see what was recorded | Medium | Medium | The attempt record shows every event and flag reason to staff. After hand-in the candidate sees the reason for every flag on their attempt, in Indonesian, on the result page and whenever they reopen their link (tested: `TestAlteredRecordEarnsNoCredential`). A self-service view of the full record is still to build (access right, art. 7) |

## 5. Rights

- **Information and consent:** per purpose, before the start, in Indonesian.
- **Access:** staff can show the full record today; candidates see every flag's reason after hand-in; a self-service view of the full record is open.
- **Objection and withdrawal:** declining before the start is built. Withdrawing mid-attempt is
  not built: today the candidate leaves the attempt and contacts the organiser. To build: a
  "stop the camera" control that ends camera processing, records the withdrawal, and flags the
  attempt for review rather than failing it.
- **Erasure:** at the end of retention (above), or on request through the organiser: the
  studio's "Erase" on a person deletes them, their attempts, credentials, uploads and mail
  (`PersonService.ErasePerson`, tested: `TestErasureRemovesEverythingAboutThePerson`).
  Backups age out on their own schedule (docs/13 §3).

## 6. Open items before a live tier 2 exam

- [ ] DPO review and sign-off of this draft.
- [ ] Detector accuracy across skin tones and lighting measured and recorded (§4).
- [ ] Mid-attempt withdrawal control (§5).
- [ ] Candidate view of their own record (§5).
- [x] Retention job live, with deletion audit.
- [ ] Customer terms: organisers offer a non-camera route on request.
