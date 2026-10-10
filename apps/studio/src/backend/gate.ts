/**
 * The way a candidate gets in: open the link, get a code, verify it, and come
 * back through the session it started. Two implementations behind one shape,
 * like repo.ts: invite.ts in this browser, or the core's CandidateService.
 *
 * The core keeps the session in an HttpOnly cookie this code cannot read, so
 * "do I still hold the session?" is a question for the server, not a value.
 */
import { ConnectError, Code } from '@connectrpc/connect'
import { api } from '../api/client'
import * as invite from '../invite'
import type { Refusal } from '../invite'
import { type Assignment, type Attempt, type Exam, listExams, listPeople, resolveExam } from '../store'
import { CORE } from './mode'
import { attemptIn, examIn, refusal } from './convert'

/** Where a candidate sits once let in: one invitation, the version it pins, and any work so far. */
export type Seat = {
  assignment: Assignment
  exam: Exam
  /** The server's attempt, when the core holds one. Local work is read separately. */
  attempt: Attempt | null
  /** Who the invitation is for, as far as this screen may say. */
  who: string
  /** Server clock minus this device's, in ms. */
  skew: number
}

export type Opened = { ok: true; email: string; stub?: string } | { ok: false; why: Refusal; email?: string }
export type CodeSent = { ok: true; retryAt: number; left: number } | { ok: false; why: Refusal | 'cooldown'; retryAt?: number }
export type Verified = { ok: true } | { ok: false; why: Refusal | 'wrong' | 'stale'; triesLeft?: number }
export type Reissued = { ok: true } | { ok: false; why: Refusal | 'cooldown' | 'limit'; retryAt?: number }

export type Gate = {
  openLink(token: string): Promise<Opened>
  requestCode(token: string): Promise<CodeSent>
  verifyCode(token: string, code: string): Promise<Verified>
  requestNewLink(token: string): Promise<Reissued>
  /** The seat this browser's session holds, or null when there is none. */
  resume(): Promise<Seat | null>
  /** Whether the session is still good: not revoked, not replaced. */
  stands(seat: Seat): Promise<boolean>
  end(): Promise<void>
}

/* ── this browser ─────────────────────────────────────────────────────────── */

/** Held only for `stands`; the local session hash is not a secret worth hiding from itself. */
let localHash: string | null = null

const local: Gate = {
  async openLink(token) {
    const r = await invite.openLink(token)
    return r.ok ? { ok: true, email: r.email, stub: r.assignment.id } : r
  },
  requestCode: invite.requestCode,
  async verifyCode(token, code) {
    const r = await invite.verifyCode(token, code)
    if (!r.ok) return r
    localHash = r.sessionHash
    return { ok: true }
  },
  requestNewLink: invite.requestNewLink,
  async resume() {
    const r = await invite.resumeSession()
    if (!r) return null
    localHash = r.sessionHash
    const current = listExams().find((e) => e.id === r.assignment.examId)
    if (!current) return null
    return {
      assignment: r.assignment,
      exam: resolveExam(current, r.assignment.examVersion),
      attempt: null,
      who: listPeople().find((p) => p.id === r.assignment.personId)?.name ?? '',
      skew: 0,
    }
  },
  async stands(seat) {
    return !!localHash && invite.sessionStands(seat.assignment.id, localHash)
  },
  async end() {
    localHash = null
    invite.endSession()
  },
}

/* ── the Go core ──────────────────────────────────────────────────────────── */

const core: Gate = {
  async openLink(token) {
    const r = await api.candidate.openLink({ token })
    if (r.ok) return { ok: true, email: r.invitation?.maskedEmail ?? '' }
    return { ok: false, why: refusal(r.refusal) as Refusal, email: r.maskedEmail || undefined }
  },
  async requestCode(token) {
    const r = await api.candidate.requestCode({ token })
    if (r.ok) return { ok: true, retryAt: Number(r.retryAt), left: r.left }
    return { ok: false, why: refusal(r.refusal) as Refusal | 'cooldown', retryAt: Number(r.retryAt) || undefined }
  },
  async verifyCode(token, code) {
    const r = await api.candidate.verifyCode({ token, code: code.replace(/\D/g, '') })
    if (r.ok) return { ok: true }
    return { ok: false, why: refusal(r.refusal) as Refusal | 'wrong' | 'stale', triesLeft: r.triesLeft }
  },
  async requestNewLink(token) {
    const r = await api.candidate.requestNewLink({ token })
    if (r.ok) return { ok: true }
    return { ok: false, why: refusal(r.refusal) as Refusal | 'cooldown' | 'limit', retryAt: Number(r.retryAt) || undefined }
  },
  async resume() {
    let r
    try {
      r = await api.candidate.getSession({})
    } catch (err) {
      if (err instanceof ConnectError && (err.code === Code.Unauthenticated || err.code === Code.NotFound)) return null
      throw err
    }
    const serverNow = Number(r.serverNow)
    const exam = examIn(r.exam!)
    const attempt = r.attempt ? attemptIn(r.attempt, serverNow) : null
    const inv = r.invitation
    const assignment: Assignment = {
      id: r.assignmentId,
      examId: exam.id,
      examVersion: exam.version,
      personId: '',
      assignedAt: 0,
      status: attempt?.submittedAt ? 'submitted' : attempt ? 'started' : 'invited',
      expiresAt: Number(inv?.expiresAt ?? 0) || undefined,
      tokenHash: '',
      retiredHashes: [],
      otpSends: 0,
      selfServes: 0,
      history: [],
    }
    return { assignment, exam, attempt, who: inv?.maskedEmail ?? '', skew: serverNow - Date.now() }
  },
  async stands() {
    try {
      await api.candidate.getSession({})
      return true
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) return false
      // Offline is not the same as revoked: let the candidate carry on.
      return true
    }
  },
  async end() {
    try {
      await api.candidate.endSession({})
    } catch {
      /* the cookie expires on its own */
    }
  },
}

export const gate: Gate = CORE ? core : local
