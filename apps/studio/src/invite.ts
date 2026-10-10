/**
 * The invitation link, end to end: minting, emailing, opening, the emailed
 * code, the session it starts, revocation.
 *
 * Everything in this file is server work. It runs in the browser only because
 * there is no server yet, and the limits it enforces are advisory for exactly
 * that reason — whoever holds the device can edit localStorage. The shapes and
 * the order of the checks are what carry over; docs/08-invite-links.md lists
 * what moves and what changes when it does.
 *
 * The rules, in one place:
 *
 * - A link token is 256 bits from the CSPRNG and is never stored. The
 *   assignment keeps its SHA-256, so a read of the store cannot produce a link.
 * - The token is opaque. Who it is for and what it opens live on the
 *   assignment, so there is nothing in the link to tamper with.
 * - It travels in the URL fragment, which is not sent to a server, not written
 *   to an access log and not passed on in Referer.
 * - Opening a link proves nothing. A 6-digit code goes to the address on file
 *   and the link is spent when that code is verified — not when the page
 *   loads, because mail scanners load pages.
 * - One live link and one session per assignment. Sending a new link retires
 *   the old one and ends the session it started.
 */
import {
  type Assignment,
  type InviteEvent,
  listAssignments,
  listExams,
  listPeople,
  writeAssignments,
} from './store'

export const LINK_TTL_MS = 7 * 24 * 60 * 60 * 1000
export const OTP_TTL_MS = 10 * 60 * 1000
export const OTP_TRIES = 5
export const OTP_COOLDOWN_MS = 60 * 1000
/** Codes per link. Five codes of five tries is 25 guesses at a million. */
export const OTP_MAX_SENDS = 5
export const SELF_SERVE_MAX = 5
const RETIRED_KEPT = 5

/* ── primitives ───────────────────────────────────────────────────────────── */

const b64url = (bytes: Uint8Array) =>
  btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')

const randomToken = () => b64url(crypto.getRandomValues(new Uint8Array(32)))

/** Six digits with no modulo bias: reject the top sliver of the 32-bit range. */
function randomCode(): string {
  const limit = Math.floor(0x1_0000_0000 / 1_000_000) * 1_000_000
  const buf = new Uint32Array(1)
  do crypto.getRandomValues(buf)
  while (buf[0]! >= limit)
  return String(buf[0]! % 1_000_000).padStart(6, '0')
}

async function sha256(text: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text))
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

/** Compares every character whatever the first difference is. */
function sameHash(a: string, b: string): boolean {
  if (a.length !== b.length || a.length === 0) return false
  let diff = 0
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i)
  return diff === 0
}

/**
 * A code has a million values, so its bare hash is a lookup table. Binding it
 * to the assignment keeps one table from serving every invitation; the server
 * version keys it with a secret instead (HMAC), which is what actually stops
 * an offline guess.
 */
const codeHash = (assignmentId: string, code: string) => sha256(`${assignmentId}:${code}`)

export const maskEmail = (email: string) => {
  const [local = '', domain = ''] = email.split('@')
  return `${local.slice(0, 1)}•••@${domain}`
}

export const inviteUrl = (token: string) => `${location.origin}${location.pathname}#/i/${token}`

/** The token from an invitation URL's fragment, or null. Nothing else is accepted. */
export const tokenFromHash = (hash: string) => hash.match(/^#\/i\/([A-Za-z0-9_-]{43})$/)?.[1] ?? null

/* ── the record ───────────────────────────────────────────────────────────── */

/**
 * Repeats of the same event fold into a count. The history is append-only, but
 * someone holding a link can open it a thousand times, and a log that grows on
 * attacker input needs a bound.
 */
function logged(a: Assignment, kind: string, detail?: string): InviteEvent[] {
  const last = a.history[a.history.length - 1]
  if (last && last.kind === kind && (last.detail ?? '').replace(/ ×\d+$/, '') === (detail ?? '')) {
    const n = Number(last.detail?.match(/ ×(\d+)$/)?.[1] ?? 1) + 1
    return [...a.history.slice(0, -1), { ...last, t: Date.now(), detail: `${detail ?? ''} ×${n}` }]
  }
  return [...a.history, { seq: a.history.length + 1, t: Date.now(), kind, detail }]
}

/** Read, change one assignment, write. Returns the changed record. */
function mutate(id: string, change: (a: Assignment) => Assignment): Assignment | null {
  const all = listAssignments()
  const i = all.findIndex((a) => a.id === id)
  if (i < 0) return null
  all[i] = change(all[i]!)
  writeAssignments(all)
  return all[i]!
}

/** Every event kind, in words an administrator can repeat to a candidate. */
export const EVENT_LABEL: Record<string, string> = {
  'link.sent': 'Invitation emailed',
  'link.resent': 'New link emailed; the previous one stopped working',
  'link.reissued': 'New link emailed at the link holder’s request',
  'link.opened': 'Link opened',
  'link.refused': 'Link opened and refused',
  'link.revoked': 'Invitation revoked',
  'link.redeemed': 'Code verified — link spent, session started',
  'code.sent': 'Verification code emailed',
  'code.wrong': 'Wrong code entered',
}

export type LinkState = 'unsent' | 'live' | 'used' | 'expired' | 'locked' | 'revoked'

const codeUsable = (a: Assignment, now: number) =>
  !!a.otp && a.otp.expiresAt > now && a.otp.tries < OTP_TRIES

/** What the link is right now. The order is the order of precedence. */
export function linkState(a: Assignment, now = Date.now()): LinkState {
  // With the core as backend the secrets are on the server, and so is the verdict.
  if (a.linkState) return a.linkState
  if (a.revokedAt) return 'revoked'
  if (!a.tokenHash) return 'unsent'
  if (a.redeemedAt) return 'used'
  if ((a.expiresAt ?? 0) <= now) return 'expired'
  if (a.otpSends >= OTP_MAX_SENDS && !codeUsable(a, now)) return 'locked'
  return 'live'
}

/**
 * A link is good until the end of the due date, or for a week when there is
 * none. Expiry stops a link being *redeemed*; it never ends a session already
 * running, because cutting someone off mid-exam is the worst thing this system
 * can do.
 */
const expiryFor = (a: Assignment, now: number) =>
  a.dueAt ? a.dueAt + 24 * 60 * 60 * 1000 - 1 : now + LINK_TTL_MS

/* ── the outbox ───────────────────────────────────────────────────────────── */

/**
 * Stands in for the mail provider and the candidate's inbox at once, which is
 * the one place a raw token or code is ever at rest. With a server the message
 * is handed to the provider and nothing here exists.
 */
export type Mail = {
  id: string
  assignmentId: string
  kind: 'invite' | 'code'
  to: string
  subject: string
  body: string
  /** Only on an invitation. */
  link?: string
  /** Only on a code. */
  code?: string
  at: number
}

const OUTBOX_KEY = 'am.outbox.v1'
const OUTBOX_KEPT = 200

export function listOutbox(): Mail[] {
  try {
    return JSON.parse(localStorage.getItem(OUTBOX_KEY) ?? '[]') as Mail[]
  } catch {
    return []
  }
}

function send(mails: Omit<Mail, 'id' | 'at'>[]) {
  const stamped = mails.map<Mail>((m) => ({ ...m, id: randomToken().slice(0, 12), at: Date.now() }))
  localStorage.setItem(OUTBOX_KEY, JSON.stringify([...stamped.reverse(), ...listOutbox()].slice(0, OUTBOX_KEPT)))
}

function inviteMail(a: Assignment, token: string, again: boolean): Omit<Mail, 'id' | 'at'> | null {
  const person = listPeople().find((p) => p.id === a.personId)
  const exam = listExams().find((e) => e.id === a.examId)
  if (!person || !exam) return null
  const link = inviteUrl(token)
  const until = new Date(a.expiresAt ?? 0).toLocaleString('id-ID', { dateStyle: 'long', timeStyle: 'short' })
  return {
    assignmentId: a.id,
    kind: 'invite',
    to: person.email,
    subject: `${again ? 'Tautan baru' : 'Undangan'}: ${exam.title}`,
    link,
    body: [
      `Halo ${person.name},`,
      '',
      `${exam.issuer} mengundang Anda mengerjakan “${exam.title}”.`,
      '',
      link,
      '',
      `Tautan ini hanya untuk Anda dan berlaku sampai ${until}. Saat membukanya kami mengirim kode 6 digit ke alamat ini untuk memastikan itu memang Anda.`,
      again ? 'Tautan sebelumnya tidak berlaku lagi.' : '',
      'Jangan teruskan email ini.',
    ]
      .filter((l, i, xs) => l !== '' || xs[i - 1] !== '')
      .join('\n'),
  }
}

/* ── sending, by an admin ─────────────────────────────────────────────────── */

/**
 * Mint a link for each assignment and mail it. Anything already live is
 * retired first, and so is the session it started. Submitted assignments and
 * instruments that are not published are skipped: there is nothing to open.
 */
export async function sendInvites(assignmentIds: string[]): Promise<number> {
  const published = new Set(listExams().filter((e) => e.status === 'published').map((e) => e.id))
  const wanted = listAssignments().filter(
    (a) => assignmentIds.includes(a.id) && a.status !== 'submitted' && published.has(a.examId),
  )
  // Hash before touching the store, so the write below is one synchronous step.
  const minted = await Promise.all(
    wanted.map(async (a) => {
      const token = randomToken()
      return { id: a.id, token, hash: await sha256(token) }
    }),
  )
  const byId = new Map(minted.map((m) => [m.id, m]))
  const now = Date.now()
  const mails: Omit<Mail, 'id' | 'at'>[] = []

  writeAssignments(
    listAssignments().map((a) => {
      const m = byId.get(a.id)
      if (!m) return a
      const again = !!a.sentAt
      const next: Assignment = {
        ...a,
        tokenHash: m.hash,
        retiredHashes: a.tokenHash ? [...a.retiredHashes, a.tokenHash].slice(-RETIRED_KEPT) : a.retiredHashes,
        sentAt: now,
        expiresAt: expiryFor(a, now),
        redeemedAt: undefined,
        revokedAt: undefined,
        sessionHash: undefined,
        otp: undefined,
        otpSends: 0,
        selfServes: 0,
        history: logged(a, again ? 'link.resent' : 'link.sent', 'by an administrator'),
      }
      const mail = inviteMail(next, m.token, again)
      if (mail) mails.push(mail)
      return next
    }),
  )
  send(mails)
  return minted.length
}

/** Kill the link and the session. The row and its history stay. */
export function revokeInvite(assignmentId: string) {
  mutate(assignmentId, (a) => ({
    ...a,
    revokedAt: Date.now(),
    tokenHash: '',
    retiredHashes: a.tokenHash ? [...a.retiredHashes, a.tokenHash].slice(-RETIRED_KEPT) : a.retiredHashes,
    sessionHash: undefined,
    otp: undefined,
    history: logged(a, 'link.revoked', 'by an administrator'),
  }))
}

/* ── opening, by whoever holds the link ───────────────────────────────────── */

export type Refusal =
  /** Not a link we ever issued. Says nothing else, on purpose. */
  | 'unknown'
  | 'revoked'
  | 'expired'
  | 'locked'
  /** Already redeemed. The holder may ask for a new one. */
  | 'used'
  /** Replaced by a newer link. The holder may ask for another. */
  | 'superseded'
  | 'submitted'
  /** The instrument is not open (unpublished or archived). */
  | 'closed'

export type Opened =
  | { ok: true; assignment: Assignment; email: string }
  | { ok: false; why: Refusal; email?: string }

async function find(token: string): Promise<{ a: Assignment; current: boolean } | null> {
  const hash = await sha256(token)
  const all = listAssignments()
  const live = all.find((a) => sameHash(a.tokenHash, hash))
  if (live) return { a: live, current: true }
  const old = all.find((a) => a.retiredHashes.some((h) => sameHash(h, hash)))
  return old ? { a: old, current: false } : null
}

/**
 * Everything short of "unknown" is told only to someone holding a token we
 * issued, so it can be specific. The address is masked regardless: a forwarded
 * link should not hand over the inbox it was sent to.
 */
async function check(token: string): Promise<Opened> {
  const hit = await find(token)
  if (!hit) return { ok: false, why: 'unknown' }
  const { a, current } = hit
  const email = maskEmail(listPeople().find((p) => p.id === a.personId)?.email ?? '')
  const exam = listExams().find((e) => e.id === a.examId)
  const refuse = (why: Refusal): Opened => ({ ok: false, why, email })

  if (a.revokedAt) return refuse('revoked')
  if (!current) return refuse('superseded')
  if (!exam || exam.status !== 'published') return refuse('closed')
  if (a.status === 'submitted') return refuse('submitted')
  const state = linkState(a)
  if (state === 'used' || state === 'expired' || state === 'locked') return refuse(state)
  return { ok: true, assignment: a, email }
}

/** The first thing a link does: say whether it is worth asking for a code. */
export async function openLink(token: string): Promise<Opened> {
  const r = await check(token)
  const hit = r.ok ? r.assignment : (await find(token))?.a
  if (hit) mutate(hit.id, (a) => ({ ...a, history: logged(a, r.ok ? 'link.opened' : 'link.refused', r.ok ? undefined : r.why) }))
  return r
}

export type CodeSent =
  | { ok: true; retryAt: number; left: number }
  | { ok: false; why: Refusal | 'cooldown'; retryAt?: number }

export async function requestCode(token: string): Promise<CodeSent> {
  const r = await check(token)
  if (!r.ok) return { ok: false, why: r.why }
  const a = r.assignment
  const now = Date.now()
  if (a.otp && now - a.otp.sentAt < OTP_COOLDOWN_MS)
    return { ok: false, why: 'cooldown', retryAt: a.otp.sentAt + OTP_COOLDOWN_MS }
  if (a.otpSends >= OTP_MAX_SENDS) return { ok: false, why: 'locked' }

  const code = randomCode()
  const hash = await codeHash(a.id, code)
  const person = listPeople().find((p) => p.id === a.personId)
  if (!person) return { ok: false, why: 'unknown' }
  mutate(a.id, (x) => ({
    ...x,
    otp: { hash, expiresAt: now + OTP_TTL_MS, tries: 0, sentAt: now },
    otpSends: x.otpSends + 1,
    history: logged(x, 'code.sent', `to ${maskEmail(person.email)}`),
  }))
  send([
    {
      assignmentId: a.id,
      kind: 'code',
      to: person.email,
      subject: `${code} adalah kode verifikasi Anda`,
      code,
      body: `Kode verifikasi Anda: ${code}\n\nBerlaku 10 menit. Jangan berikan kode ini kepada siapa pun — kami tidak akan pernah memintanya.`,
    },
  ])
  return { ok: true, retryAt: now + OTP_COOLDOWN_MS, left: OTP_MAX_SENDS - a.otpSends - 1 }
}

export type Verified =
  | { ok: true; assignment: Assignment; sessionHash: string }
  | { ok: false; why: Refusal | 'wrong' | 'stale'; triesLeft?: number }

/**
 * Checks the code and, if it is right, spends the link and starts the session
 * in the same write. A wrong guess costs a try whether or not it was close.
 */
export async function verifyCode(token: string, typed: string): Promise<Verified> {
  const r = await check(token)
  if (!r.ok) return { ok: false, why: r.why }
  const a = r.assignment
  const code = typed.replace(/\D/g, '')
  if (!a.otp || a.otp.expiresAt <= Date.now() || a.otp.tries >= OTP_TRIES) return { ok: false, why: 'stale' }

  if (code.length !== 6 || !sameHash(a.otp.hash, await codeHash(a.id, code))) {
    const next = mutate(a.id, (x) => ({
      ...x,
      otp: x.otp && { ...x.otp, tries: x.otp.tries + 1 },
      history: logged(x, 'code.wrong'),
    }))
    if (next && linkState(next) === 'locked') return { ok: false, why: 'locked' }
    return { ok: false, why: 'wrong', triesLeft: OTP_TRIES - (next?.otp?.tries ?? OTP_TRIES) }
  }

  const secret = randomToken()
  const sessionHash = await sha256(secret)
  const next = mutate(a.id, (x) => ({
    ...x,
    redeemedAt: Date.now(),
    otp: undefined,
    sessionHash,
    history: logged(x, 'link.redeemed', 'code verified; session started'),
  }))
  if (!next) return { ok: false, why: 'unknown' }
  localStorage.setItem(SESSION_KEY, JSON.stringify({ assignmentId: a.id, secret }))
  return { ok: true, assignment: next, sessionHash }
}

export type Reissued = { ok: true } | { ok: false; why: Refusal | 'cooldown' | 'limit'; retryAt?: number }

/**
 * The way back in for someone whose link is spent — a crashed browser, a
 * second device. Holding an old link earns one thing: a new link mailed to the
 * address on file. It does not extend the deadline and it ends the old session.
 */
export async function requestNewLink(token: string): Promise<Reissued> {
  const r = await check(token)
  if (r.ok) return { ok: true } // still good; nothing to replace
  if (r.why !== 'used' && r.why !== 'superseded') return { ok: false, why: r.why }
  const a = (await find(token))!.a
  const now = Date.now()
  if ((a.expiresAt ?? 0) <= now) return { ok: false, why: 'expired' }
  if (a.status === 'submitted') return { ok: false, why: 'submitted' }
  if (now - (a.sentAt ?? 0) < OTP_COOLDOWN_MS) return { ok: false, why: 'cooldown', retryAt: (a.sentAt ?? 0) + OTP_COOLDOWN_MS }
  if (a.selfServes >= SELF_SERVE_MAX) return { ok: false, why: 'limit' }

  const fresh = randomToken()
  const hash = await sha256(fresh)
  const next = mutate(a.id, (x) => ({
    ...x,
    tokenHash: hash,
    retiredHashes: x.tokenHash ? [...x.retiredHashes, x.tokenHash].slice(-RETIRED_KEPT) : x.retiredHashes,
    sentAt: now,
    redeemedAt: undefined,
    sessionHash: undefined,
    otp: undefined,
    otpSends: 0,
    selfServes: x.selfServes + 1,
    history: logged(x, 'link.reissued', 'asked for by the link holder; mailed to the address on file'),
  }))
  const mail = next && inviteMail(next, fresh, true)
  if (mail) send([mail])
  return { ok: true }
}

/* ── the session ──────────────────────────────────────────────────────────── */

/**
 * What a redeemed link leaves on the device. A server would set this as an
 * HttpOnly, Secure, SameSite=Strict cookie the page cannot read; localStorage
 * is the nearest thing that survives a killed browser, which matters more here.
 */
const SESSION_KEY = 'am.invite-session.v1'

export async function resumeSession(): Promise<{ assignment: Assignment; sessionHash: string } | null> {
  try {
    const raw = JSON.parse(localStorage.getItem(SESSION_KEY) ?? 'null') as { assignmentId: string; secret: string } | null
    if (!raw) return null
    const sessionHash = await sha256(raw.secret)
    const a = listAssignments().find((x) => x.id === raw.assignmentId)
    if (!a || !sessionStands(a.id, sessionHash)) {
      endSession()
      return null
    }
    return { assignment: a, sessionHash }
  } catch {
    return null
  }
}

/** Cheap enough to ask before every step that matters: start, submit. */
export function sessionStands(assignmentId: string, sessionHash: string): boolean {
  const a = listAssignments().find((x) => x.id === assignmentId)
  return !!a && !a.revokedAt && !!a.sessionHash && sameHash(a.sessionHash, sessionHash)
}

export function endSession() {
  try {
    localStorage.removeItem(SESSION_KEY)
  } catch { /* ignore */ }
}
