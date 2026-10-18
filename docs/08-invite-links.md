# Candidate Invitations

How a candidate gets from an administrator's roster to an exam, and what has to move to a
server before any of it is real. The prototype in `apps/studio/` runs the whole flow in the browser
against `localStorage`; `apps/studio/src/invite.ts` is written as the server it stands in for.

## 1. Getting people in

An administrator invites people from **Assignments** (or adds them to **People** first):

| Route | What happens |
| :--- | :--- |
| One by one | Pick from the roster, or type `Name <email>` lines under "Invite someone not on the roster". New addresses join the roster. |
| CSV | `.csv`, `.tsv` or `.txt`, comma, semicolon or tab. A header row with `name`, `email` and `group` columns (English or Indonesian names) in any order; without a header, the cell with an `@` is the email. |
| Excel | `.xlsx`, first sheet. The old `.xls` format is refused with a "save as .xlsx" message. |

Every file is previewed before anything is written: new, already on the roster, repeated in
the file, and each unusable row with its row number. Only name, email and group are kept —
any other column (a national id, a date of birth) is dropped in `apps/studio/src/roster.ts` and never
stored. Caps: 5 MB per file, 5,000 rows, 30 MB inflated per workbook entry (zip bombs).

## 2. The link

- **256 random bits** from the CSPRNG, base64url, 43 characters. Only its SHA-256 is stored on
  the assignment, so nobody — administrators included — can read a link back out of the
  system. There is no "copy link"; **Resend** mints a new link and retires the old one.
- **Opaque.** Who it is for, which exam and which pinned version live on the assignment row.
- **In the URL fragment** (`/#/i/<token>`): not sent to the server, not in access logs, not in
  `Referer`. The app strips it from the address bar on arrival.
- **Expires** at the end of the due date, or 7 days after sending when there is none.
  Expiry stops a link being redeemed; it never ends a session already running.
- **Revocable** at any time. Revoking kills the link and any session it started; the row and
  its history stay.

## 3. Opening it

1. The holder sees only a masked address (`b•••@example.id`) and a "send code" button.
2. A 6-digit code goes to the address on file: 10-minute life, 5 tries per code, 60-second
   resend cooldown, 5 codes per link before the link locks.
3. The link is spent when the code is verified, not when the page loads, so mail scanners
   that prefetch links cannot burn it.
4. Verifying starts **one session per invitation**. Reloading or reopening a killed browser
   comes back through that session (`#/session`), with saved answers intact.
5. A spent or replaced link offers "send me a new link", which only ever mails the address
   on file (at most 5 times), and ends the old session.

Every step lands in the assignment's invite history, shown to the administrator in plain
words. Repeats fold into a count so a link opened a thousand times cannot grow the log.

**Known limit:** the code and the link go to the same inbox, so the code protects against a
leaked or forwarded link, not a compromised mailbox. Tier 3 exams add identity matching on
top of this.

## 4. What moved to the backend

The local build still runs the whole flow in the browser, and nothing there is a security
boundary: whoever holds the device can edit `localStorage`. Against the core (`services/core`)
every row below is enforced by the server, with the same shapes and the same order of checks.

| Prototype (local build) | Core | Where |
| :--- | :--- | :--- |
| `sendInvites`, `revokeInvite`, `openLink`, `requestCode`, `verifyCode`, `requestNewLink` in `invite.ts` | Connect methods on `AssignmentService` and `CandidateService`; the client keeps only the screens | `internal/api/invite.go`, `candidate.go` |
| Assignments, people, history in `localStorage` | Postgres under row-level security on `tenant_id`; history is an append-only table | `internal/db/migrations` |
| Code hash = SHA-256(`assignmentId:code`) | HMAC-SHA-256 under a key derived from `AM_SECRET`, one key each for links, codes and sessions, so a stolen table cannot be brute-forced offline | `internal/secure` |
| Limits (tries, cooldown, sends) counted on the row | The same counters, read `FOR UPDATE` and written in one transaction; then a per-address limit (`AM_LIMIT_PER_IP`, 60 a minute) on every link call and a per-tenant limit (`AM_LIMIT_PER_TENANT`, 600 a minute) on codes mailed or guessed and links reissued | `candidate.go`, `internal/limit` |
| Session secret in `localStorage` | `HttpOnly`, `Secure`, `SameSite=Strict` cookie whose HMAC is stored on the assignment, so revoking or resending ends it | `internal/auth` |
| Outbox panel on Assignments, stub code in the candidate preview bar | Outbox row and delivery job written in the same transaction, sent by a worker, body cleared once sent. Both stubs are deleted: the studio shows no mail in any build | `internal/api/outbox.go`, `internal/mail` |
| Roster files parsed in the browser | Parsed in the browser and re-validated on the server; the raw file is never stored | `internal/api/people.go` |
| Emails, names in one store | PII kept in its region; attempt records and sent mail expire, and a person can be erased. **Not yet:** a retention rule for unused roster entries and expired invitations | `internal/api/retention.go` |

A local build has no mail at all: its stand-in inbox is in storage, read by the browser test,
and logged to the console in development. Nothing on any screen shows a link or a code.

The limits are counted in each server process. One machine (the pilot's shape) enforces them
exactly; behind several replicas each enforces its own share, while the per-row counters stay
exact because they live in Postgres. An exam hall behind one NAT address makes about four
calls per candidate in the minute everyone gets in: raise `AM_LIMIT_PER_IP` to cover the room.

Proven in CI by `services/core/internal/api/invite_test.go` (a revoked or replaced link cannot
start or submit; 25 wrong guesses lock the link; one tenant's limit does not slow another),
`isolation_test.go` (every invite method is tenant-scoped), `outbox_test.go`, and the
kill-the-browser gate in `apps/studio/e2e-core.mjs`, which runs with the real session cookie.
