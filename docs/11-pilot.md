# 11 · Pilot runbook

The last step of docs/09 phase 6: a dry run with internal candidates, then the
design partner's live exam. **Gate:** the partner accepts the results in
writing. This page is the plan for both; the code it relies on is in place, the
people and dates are not.

## 1. Before the dry run

- [ ] Production stack up in an Indonesia region (deploy track), with the
      go-live checklist in docs/09 §4 worked through. Anything still open is
      written down with who accepted it.
- [ ] `AM_SECRET` generated for production and stored only in the secret store.
      It derives the attempt seal and the key that protects each tenant's
      credential signing key: rotating it breaks both, so it is not rotated
      during the pilot.
- [ ] The partner's tenant created (`core tenant "<name>" <staff email>`), its
      region set, staff invited.
- [ ] DPIA (docs/10) signed off by the DPO if the exam uses tier 2. Otherwise
      run tier 1, which needs no camera.
- [ ] The exam authored and reviewed by the partner, or imported from their QTI
      package; previewed as a candidate on a low-end Android phone.

## 2. Dry run (internal candidates, 10 to 30 people)

Run it as the real thing: invitations by email, the real deadline, the real
tier.

During it, someone watches the proctor monitor and writes down anything
surprising. Partway through, two people turn their network off for five
minutes and carry on; one closes the browser and reopens the link.

Afterwards, check:

- [ ] Every sitting handed in, with nothing lost. The network and kill cases
      above are the ones to look at.
- [ ] Every flag raised has a reason that the person it concerns agrees
      describes what they did. Any reason they would contest in good faith is
      a bug in the rule, not in them.
- [ ] Essays scored blind in the Scoring screen. Each candidate's credential
      arrived by email and verifies at its link.
- [ ] Item analysis reads sensibly for the size: with fewer than 30 hand-ins it
      is labelled provisional, and only a negative discrimination is acted on
      (check that key).
- [ ] A restore drill run on the production database (data-safety track) after
      the dry run's data is in it.

## 3. The live exam

- **T-7 days:** invitations sent. The monitor shows who has opened theirs.
  People who have not opened theirs are re-sent from Assignments.
- **T-1 day:** the on-call person and the partner's contact are named, and the
  incident runbook is to hand.
- **During:** the monitor stays open. A sitting that goes quiet is not an
  emergency: the device keeps the answers, and the server's clock decides the
  deadline (answers made offline before the deadline count for up to 30
  minutes after it).
- **After:** essays scored, then results reviewed with the partner: scores, the
  flags and their reasons, and item analysis. Credentials go out as each
  result becomes final.

## 4. What "accepted in writing" covers

The partner confirms, for this exam:

1. The scores are right. Spot-check at least five attempts against their sealed
   records in the monitor.
2. Every flag that affected a decision had a reason they could explain to the
   candidate.
3. The data stayed in the agreed region, and the consent text matched what was
   collected.
4. The credentials verify, and name no one in the clear.

Keep the acceptance with the pilot record. Anything they would not accept goes
back into docs/09 as work before the next exam.
