# 12 · Security: controls in place and the external test's scope

Status: written for the security track of docs/09. Section 3 is the brief for the external
penetration test before the first paid exam; sections 1–2 are what the testers should find.

## 1. Controls in place

| Area | Control | Where |
| :--- | :--- | :--- |
| Tenant isolation | Every tenant table has row level security keyed on `app.tenant_id`; the app connects as a role that cannot bypass it; cross-tenant lookups by id are `SECURITY DEFINER` functions that return ids only. Every RPC is in `TestTenantIsolation` | `internal/db/migrations`, `internal/api/isolation_test.go` |
| Authorisation | Each procedure has a policy entry (public, staff, admin, super admin, candidate); a procedure without one cannot be reached | `internal/api/server.go` `Policy()`, `internal/auth` |
| Staff across tenants | A staff row is one membership of one tenant, with a role (admin or member). A session is in one tenant at a time; switching mints a session in the other tenant and revokes the old one. Admin procedures are refused at the interceptor from the session's role, and a request that reaches another tenant (granting access, sharing an instrument) checks the caller is an admin there too. Withdrawn access ends that tenant's sessions at once; a tenant always keeps one admin. Super administrators (by address, in `super_admins`, which the app role cannot read or write except through `SECURITY DEFINER` functions) alone create tenants and grant, change or withdraw the admin role, in any tenant; an admin adds and removes members of the tenants they administer only. The flag is read with the session on every request, so withdrawing it applies at once. Webhooks, which send candidate data out and show a signing secret, are managed by admins only | `internal/api/tenants.go`, `0007_tenancy.sql`, `0008_super_admin.sql`, `internal/api/tenancy_test.go` |
| Sharing instruments | A shared version is copied into each target tenant as that tenant's own instrument and published there; its images are copied into the target's storage, never read across tenants. Where it came from is not in the frozen version candidates are given | `internal/api/tenants.go` `ShareExam` |
| Staff sign-in | Emailed one-time code, hashed at rest, short-lived; session cookie `HttpOnly`, `Secure`, `SameSite=Strict`, 12 h | `internal/api/authsvc.go` |
| Candidate access | Invite links carry a random token, hashed at rest; one attempt per assignment | `internal/api/invite.go` |
| Cross-site requests | `SameSite=Strict` plus an `Origin` check on every non-GET | `cmd/core/main.go` `sameOrigin` |
| Abuse | Per-IP and per-email limits on the sign-in and link requests (in memory per instance, see §4) | `internal/limit` |
| Browser | Content policy per app: own origin only, no inline script, no framing; WebAssembly and the camera allowed only to the runner; `nosniff`, HSTS, COOP | `cmd/core/headers.go` |
| Attempt record | Append-only, HMAC hash-chained; editing or dropping an event is detected on read | `internal/domain/attempt.go` |
| Secrets at rest | Issuer signing keys sealed with AES-GCM under a key derived from `AM_SECRET`, labelled per purpose | `internal/secure` |
| Uploads | Images only, re-checked by content, size capped, served with their checked type | `internal/api/media.go` |
| Logs | Method, path, status, duration; never bodies (answers, tokens) | `cmd/core/main.go` `logRequests` |
| Supply chain | `bun audit`, `govulncheck`, `pip-audit` and a gitleaks history scan on every PR | `.github/workflows/ci.yml` `supply-chain` |

## 2. Threat model, in short

The assets, in order: candidates' answers and integrity records (personal data, decides
someone's job or grade), exam content before an exam (leak = cheating), issued credentials
(forgery = fraud), staff sessions (access to all of the above in one tenant).

The actors: a candidate who wants a better result, a candidate who wants another candidate's
data, a staff member of tenant A who wants tenant B's data, an outsider with a leaked link,
and an outsider with nothing.

## 3. Scope for the external test

In scope, against a staging deployment with two seeded tenants and test accounts in each:

1. **Tenant isolation.** With tenant A's staff session, read or change anything of tenant B:
   exams, items, people, attempts, credentials, media, live monitor streams.
2. **Candidate to candidate.** With one invite link, reach another candidate's attempt, answers
   or record; reuse, guess or replay a link; start a second attempt.
3. **Exam content before release.** As a candidate, read items of an exam not yet opened, or the
   answer key at any time (the key must never reach the runner).
4. **Score and record tampering.** Change one's own score, alter or drop attempt events, add
   integrity events without consent, submit after the deadline, replay a submission.
5. **Credentials.** Forge or alter a credential that the verify page accepts; un-revoke one;
   learn a holder's email from a credential.
6. **Sign-in.** Code guessing within the rate limits, session fixation, cookie scope, sign-out
   really ending the session, enumeration of staff emails.
7. **Browser.** XSS through item text, essay answers, names, notes or uploaded images, in the
   studio, runner and verify page; clickjacking; the content policy.
8. **Uploads.** Polyglot and oversized files, content-type confusion, path traversal.
9. **Offline runner.** What the service worker and the local queue keep on a shared computer
   after the attempt ends.

Out of scope: denial of service by volume (load is tested separately, docs/09), the mail and
cloud providers themselves, social engineering of staff.

Rules: staging only, no real candidate data, findings reported privately to the engineering
lead; critical findings (cross-tenant read, score change, credential forgery) are reported the
same day.

## 4. Known gaps, to close or accept before go-live

- Rate limits are per instance. With more than one core replica, put the limit at the proxy or
  move it to Postgres.
- The content policy allows inline styles (the component library injects them). Moving to
  nonces needs the library's nonce support; until then script is the strict part.
- `AM_SECRET` rotation re-seals the issuer keys; the procedure is not written yet.
- A candidate-facing view of their own record (DPIA §5) is not built.
