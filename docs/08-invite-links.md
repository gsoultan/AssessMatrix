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

## 4. What moves to the backend

Nothing below is a security boundary while it runs in the browser: whoever holds the device
can edit `localStorage`. The shapes and the order of checks carry over unchanged.

| Today (prototype) | With a server |
| :--- | :--- |
| `sendInvites`, `revokeInvite`, `openLink`, `requestCode`, `verifyCode`, `requestNewLink` in `invite.ts` | Connect RPC methods on an `InviteService`; the client keeps only the screens |
| Assignments, people, history in `localStorage` | Postgres, every query scoped by `tenant_id`; history is an append-only table |
| Code hash = SHA-256(`assignmentId:code`) | HMAC-SHA-256 with a server secret, so a stolen table cannot be brute-forced offline |
| Limits (tries, cooldown, sends) counted on the row | Same counters, enforced in a transaction, plus per-IP and per-tenant rate limits |
| Session secret in `localStorage` | `HttpOnly`, `Secure`, `SameSite=Strict` cookie bound to the assignment |
| Outbox panel on Assignments, stub code in the candidate preview bar | A mail provider via the outbox pattern (row written in the same transaction, sent by a worker, retried idempotently); both stubs are deleted |
| Roster files parsed in the browser | Parsed server-side with the same caps, or in the browser and re-validated on the server; the raw file is never stored |
| Emails, names in one store | PII kept in its region (the `sec` and `privacy` vetoes in `AGENTS.md`); a retention rule for unused roster entries and expired invitations |

Before launch the server version needs: a tenant-isolation test on every invite method, a
test that a revoked or replaced link cannot start or submit, a lock test (25 wrong guesses),
and the existing kill-the-browser test run against the real session cookie.
