import { useEffect } from 'react'
import { create } from 'zustand'
import type { Media } from './media'
import {
  type Attempt,
  type Exam,
  type Item,
  type Kind,
  type Status,
  type Person,
  type Assignment,
  type PersonDraft,
  addResponse,
  allItems,
  append,
  clearAttempt,
  clearResponses,
  getAttempt,
  isSurvey,
  listResponses,
  newAttempt,
  parsePeopleText,
  saveAttempt,
  score,
  unanswered,
  updateAssignment,
  listAssignments,
  listExams,
  listPeople,
  backfillVersions,
} from './store'
import { tokenFromHash } from './invite'
import { CORE } from './backend/mode'
import { type SaveHow, explain, isSignedOut, repo } from './backend/repo'
import { type Seat, gate } from './backend/gate'
import { type Pending, enqueue, flush, forget, open as openQueue, pendingCount, pendingEvents, replay } from './backend/sync'
import { type Staff, staffAuth } from './backend/staff'
import { whenRefused } from './backend/onetab'
import type { Signal } from './backend/signals'
import { type Purpose, collects } from './integrity'
import { api } from './api/client'
import { attemptIn } from './backend/convert'

/**
 * Shared application state.
 *
 * `backend/repo.ts` is the persistence layer: this browser for the prototype,
 * the Go core over Connect for a real deployment. This is the in-memory view
 * of it that every screen reads, and the only code that calls it.
 *
 * Two reasons it exists, stated honestly:
 *
 * 1. Every screen used to take its own `useState(() => listExams())` snapshot.
 *    One source of truth removes the staleness trap that sets up.
 *
 * 2. Side effects belong in these actions. Zustand actions run outside React's
 *    render machinery, so unlike a `setState` updater they are NOT double-
 *    invoked under StrictMode — which is exactly how a questionnaire response
 *    once got recorded twice here.
 *
 * Every write is async, because against the core it is a network call. A
 * write that fails says why in `notice` and resolves to null (or zero), so a
 * screen never has to catch.
 */
type AppState = {
  /* ---- who is using this ---- */
  /** undefined while checking, null when signed out. Always set in a local build. */
  staff: Staff | null | undefined
  /** Check the session and load everything a signed-in staff member sees. */
  boot: () => Promise<void>
  signedIn: (s: Staff) => Promise<void>
  signOut: () => Promise<void>
  /** The last thing that went wrong, in words a person can act on. */
  notice: string
  dismissNotice: () => void

  /* ---- navigation ----
     Which screen is active lives here, not in the shell, so a screen can send
     you somewhere else — "open this exam in the editor" is a navigation event
     plus a selection, and both belong in one place. */
  screen: string
  setScreen: (key: string) => void
  /**
   * This tab belongs to a candidate who came by link. Against the core they
   * get the candidate app alone: no staff shell, no sign-in.
   */
  candidateView: boolean
  /** The instrument the editor is editing. */
  currentExamId: string | null
  /** Set by "preview as candidate"; consumed and cleared by the candidate app. */
  previewExamId: string | null
  openInEditor: (id: string) => void
  openAssignments: (id: string) => void
  previewAsCandidate: (id: string) => void
  takePreview: () => string | null

  /* ---- catalogue ---- */
  exams: Exam[]
  responses: Record<string, Attempt[]>
  reload: () => Promise<void>
  /**
   * 'draft' — save the working copy.
   * 'same'  — overwrite the version candidates are sitting on.
   * 'new'   — branch the working copy to version + 1, leaving the live one alone.
   */
  saveExam: (e: Exam, how: SaveHow) => Promise<Exam | null>
  /** Save the working copy, then make it the version candidates get. */
  publishExam: (e: Exam) => Promise<Exam | null>
  createExam: (kind: Kind) => Promise<Exam | null>
  /** A QTI package → a new draft. Anything left behind is said in `notice`. */
  importExam: (file: File) => Promise<Exam | null>
  /** Download the exam as a QTI 3.0 package. */
  exportExam: (id: string) => Promise<void>
  duplicateExam: (id: string) => Promise<Exam | null>
  setStatus: (id: string, status: Status) => Promise<Exam | null>
  /** Archiving is the safe default; `destroyExam` is the irreversible one. */
  removeExam: (id: string) => Promise<void>
  /** Hard delete. Only reachable from the archive, never from the main list. */
  destroyExam: (id: string) => Promise<void>
  loadResponses: (examId: string) => Promise<void>
  clearResponsesFor: (examId: string) => void

  /* ---- people and assignment ---- */
  people: Person[]
  assignments: Assignment[]
  addPerson: (name: string, email: string, group: string) => Promise<void>
  importRoster: (text: string) => Promise<{ added: number; skipped: number }>
  /** Rows already parsed and validated from a file. Returns how many were new. */
  addPeople: (drafts: PersonDraft[]) => Promise<number>
  removePerson: (id: string) => Promise<void>
  /** Erase everything about a person; returns false if the core refused. */
  erasePerson: (id: string, reason: string) => Promise<boolean>
  /**
   * Assign, and email each new assignee their link. `sent` is 0 for a draft:
   * an invitation to something that cannot be opened is held, not mailed.
   */
  assign: (examId: string, personIds: string[], dueAt?: number) => Promise<Invited>
  /** Invite addresses that may not be on the roster; new ones are added to it. */
  assignByEmail: (examId: string, text: string, dueAt?: number) => Promise<Invited>
  /** The same, for people read from a file. */
  assignDrafts: (examId: string, drafts: PersonDraft[], dueAt?: number) => Promise<Invited>
  unassignOne: (id: string) => Promise<void>
  /** Mint and mail a new link, retiring the old one. Returns how many went out. */
  sendInvite: (assignmentIds: string[]) => Promise<number>
  revokeInviteFor: (assignmentId: string) => Promise<void>
  /** Re-read people and assignments after the link layer has written to them. */
  syncDelivery: () => Promise<void>
  /**
   * The token from an invitation URL, held in memory for the moment between
   * arriving and being checked. It is never persisted, and it is taken out of
   * the address bar as soon as it is read.
   */
  inviteToken: string | null
  takeInviteToken: () => string | null

  /* ---- the live attempt ---- */
  /** The invitation this tab acts for, once its code is verified. */
  seat: Seat | null
  /** Take a seat: this tab now acts for one invitation and nothing else. */
  admit: (seat: Seat) => void
  /** Set when the server says the session is gone (revoked, or opened elsewhere). */
  sessionLost: boolean
  /** Set while answers are waiting for a connection. */
  offline: boolean
  attempt: Attempt | null
  /** Start the attempt with the purposes the candidate agreed to (see integrity.ts). */
  beginAttempt: (exam: Exam, assignment?: Assignment, consents?: Purpose[]) => Promise<void>
  /** Record an integrity signal (already filtered by tier; see backend/signals.ts). */
  signal: (s: Signal) => void
  resumeAttempt: (exam: Exam, assignmentId?: string) => boolean
  discardAttempt: (examId: string, assignmentId?: string) => void
  setChoice: (item: Item, choiceId: string, n: number) => void
  setText: (item: Item, value: string, n: number) => void
  setFile: (item: Item, media: Media | undefined) => void
  toggleFlag: (item: Item, n: number) => void
  tick: () => void
  finalizeAttempt: (exam: Exam, force: boolean) => Promise<Finalized>
}

export type Invited = { invited: number; created: number; sent: number }

/**
 * - ok: submitted.
 * - missing: required questions are blank; their ids, in order.
 * - offline: answers have not all reached the server; nothing is lost, try again.
 * - session: the invitation was revoked or opened elsewhere.
 */
export type Finalized = { ok: true } | { ok: false; missing: string[] } | { ok: false; why: 'offline' | 'session' }

/** Where a candidate's tab sits once their link is redeemed. Carries no secret. */
export const SESSION_HASH = '#/session'

/** Read an invitation token out of the address bar and take it straight back out. */
function arriveByLink(): string | null {
  const token = tokenFromHash(location.hash)
  if (token) history.replaceState(null, '', location.pathname + location.search)
  return token
}

const bootToken = arriveByLink()

/** A local build reads this browser synchronously, so its first render already has data. */
const localBoot = () => {
  if (CORE) return { exams: [], people: [], assignments: [] }
  const exams = listExams()
  backfillVersions(exams)
  return { exams, people: listPeople(), assignments: listAssignments() }
}
/** A candidate arrives on the candidate app, by link or by reloading mid-exam. */
export const arrivedAsCandidate = () => !!bootToken || location.hash === SESSION_HASH

/* ── the remote attempt's outbound queue ─────────────────────────────────── */

let flushTimer: number | undefined
let signalTimer = 0
let retryTimer: number | undefined

export const useApp = create<AppState>((set, get) => {
  /** Run a write; on failure say why and hand back the fallback. */
  async function run<T>(fallback: T, fn: () => Promise<T>): Promise<T> {
    try {
      return await fn()
    } catch (err) {
      if (CORE && isSignedOut(err)) set({ staff: null })
      set({ notice: explain(err) })
      return fallback
    }
  }

  const refreshExams = async () => set({ exams: await repo.listExams() })
  const refreshDelivery = async () => {
    const [people, assignments] = await Promise.all([repo.listPeople(), repo.listAssignments()])
    set({ people, assignments })
  }
  const putExam = (e: Exam) => {
    const xs = get().exams
    set({ exams: xs.some((x) => x.id === e.id) ? xs.map((x) => (x.id === e.id ? e : x)) : [e, ...xs] })
  }

  /** Persist a local attempt and show it. A server attempt is persisted by its queue. */
  const commit = (next: Attempt) => {
    if (!next.id) saveAttempt(next)
    set({ attempt: next })
  }

  const scheduleFlush = (attemptId: string, delay = 600) => {
    window.clearTimeout(flushTimer)
    flushTimer = window.setTimeout(() => void drain(attemptId), delay)
  }

  const drain = async (attemptId: string) => {
    window.clearTimeout(retryTimer)
    const r = await flush(attemptId)
    const a = get().attempt
    if (!r.ok && r.why === 'session') {
      set({ sessionLost: true })
      return r
    }
    if (!r.ok && r.why === 'offline') {
      set({ offline: true })
      retryTimer = window.setTimeout(() => void drain(attemptId), 5000)
    } else set({ offline: false })
    if (a?.id === attemptId) {
      const seat = get().seat
      if (r.ok && seat) set({ seat: { ...seat, skew: r.serverNow - Date.now() } })
      set({ attempt: { ...a, queued: pendingCount(attemptId), deadlineAt: (r.ok && r.deadlineAt) || a.deadlineAt } })
    }
    return r
  }

  /** Record an answer-changing event for a server attempt. */
  const record = (a: Attempt, ev: Omit<Pending, 'clientTime'>) => {
    if (!a.id) return
    enqueue(a.id, { ...ev, clientTime: Date.now() })
    scheduleFlush(a.id, ev.kind === 'response.typed' ? 1200 : 300)
  }

  const queuedFor = (a: Attempt) => (a.id ? pendingCount(a.id) : a.queued + 1)

  // Another tab tried to open this sitting and was turned away: on the record.
  whenRefused(() => {
    const a = get().attempt
    if (a?.id && !a.submittedAt) {
      if (!collects(get().seat?.exam.tier ?? 0, 'session.second_tab')) return
      record(a, { kind: 'session.second_tab', detail: 'another tab tried to open this sitting and was refused' })
      set({ attempt: { ...a, queued: queuedFor(a) } })
    }
  })

  // Back online: send what is waiting now rather than at the next retry.
  window.addEventListener('online', () => {
    const id = get().attempt?.id
    if (id && !get().attempt?.submittedAt) void drain(id)
  })

  return {
    staff: CORE ? undefined : { id: 'local', email: '', name: 'This browser', tenantName: 'Local data' },
    notice: '',
    dismissNotice: () => set({ notice: '' }),

    boot: async () => {
      if (!CORE) return
      const staff = await staffAuth.whoAmI()
      set({ staff })
      if (staff) await run(undefined, async () => set(await repo.load()))
    },
    signedIn: async (staff) => {
      set({ staff, notice: '' })
      await run(undefined, async () => set(await repo.load()))
    },
    signOut: async () => {
      await staffAuth.signOut()
      set({ staff: null, exams: [], people: [], assignments: [], responses: {}, screen: 'library' })
    },

    screen: arrivedAsCandidate() ? 'player' : 'library',
    candidateView: arrivedAsCandidate(),
    currentExamId: null,
    previewExamId: null,

    setScreen: (key) => {
      // Leaving the candidate app is leaving the candidate's session behind.
      if (key !== 'player' && location.hash === SESSION_HASH) {
        void gate.end()
        set({ seat: null, attempt: null })
        history.replaceState(null, '', location.pathname + location.search)
      }
      set({ screen: key })
    },
    openInEditor: (id) => set({ currentExamId: id, screen: 'builder' }),
    openAssignments: (id) => set({ currentExamId: id, screen: 'assignments' }),
    previewAsCandidate: (id) => set({ previewExamId: id, screen: 'player' }),
    takePreview: () => {
      const id = get().previewExamId
      if (id) set({ previewExamId: null })
      return id
    },

    ...localBoot(),
    responses: {},
    attempt: null,
    seat: null,
    sessionLost: false,
    offline: false,
    inviteToken: bootToken,

    addPerson: (name, email, group) =>
      run(undefined, async () => {
        await repo.ensurePeople([{ name, email, group }])
        set({ people: await repo.listPeople() })
      }),

    importRoster: (text) =>
      run({ added: 0, skipped: 0 }, async () => {
        const drafts = parsePeopleText(text)
        const { created } = await repo.ensurePeople(drafts)
        if (created) set({ people: await repo.listPeople() })
        return { added: created, skipped: drafts.length - created }
      }),

    addPeople: (drafts) =>
      run(0, async () => {
        const { created } = await repo.ensurePeople(drafts)
        if (created) set({ people: await repo.listPeople() })
        return created
      }),

    removePerson: (id) =>
      run(undefined, async () => {
        await repo.deletePerson(id)
        await refreshDelivery()
      }),

    erasePerson: (id, reason) =>
      run(false, async () => {
        const r = await repo.erasePerson(id, reason)
        await refreshDelivery()
        set({ notice: `Erased: ${r.attempts} attempt(s), ${r.credentials} credential(s), ${r.images} image(s).` })
        return true
      }),

    assign: (examId, personIds, dueAt) =>
      run({ invited: 0, created: 0, sent: 0 }, async () => {
        const r = await repo.assign(examId, personIds, dueAt)
        await refreshDelivery()
        return { ...r, created: 0 }
      }),

    assignByEmail: async (examId, text, dueAt) => get().assignDrafts(examId, parsePeopleText(text), dueAt),

    assignDrafts: (examId, drafts, dueAt) =>
      run({ invited: 0, created: 0, sent: 0 }, async () => {
        const { ids, created } = await repo.ensurePeople(drafts)
        const r = await repo.assign(examId, ids, dueAt)
        await refreshDelivery()
        return { ...r, created }
      }),

    unassignOne: (id) =>
      run(undefined, async () => {
        await repo.unassign(id)
        set({ assignments: await repo.listAssignments() })
      }),

    sendInvite: (assignmentIds) =>
      run(0, async () => {
        const n = await repo.sendInvites(assignmentIds)
        set({ assignments: await repo.listAssignments() })
        return n
      }),

    revokeInviteFor: (assignmentId) =>
      run(undefined, async () => {
        await repo.revokeInvite(assignmentId)
        set({ assignments: await repo.listAssignments() })
      }),

    syncDelivery: () => run(undefined, refreshDelivery),

    takeInviteToken: () => {
      const t = get().inviteToken
      if (t) set({ inviteToken: null })
      return t
    },

    reload: () => run(undefined, refreshExams),

    saveExam: (e, how) =>
      run(null, async () => {
        const saved = await repo.saveExam(e, how)
        putExam(saved)
        return saved
      }),

    publishExam: (e) =>
      run(null, async () => {
        // A live version is changed in place; anything else is saved first and
        // then published, which is what freezes it.
        const live = e.status === 'published' && e.publishedVersion === e.version
        const saved = await repo.saveExam(e, live ? 'same' : 'draft')
        const published = live ? saved : await repo.setStatus(saved.id, 'published')
        putExam(published)
        return published
      }),

    createExam: (kind) =>
      run(null, async () => {
        const e = await repo.createExam(kind)
        putExam(e)
        set({ currentExamId: e.id })
        return e
      }),

    importExam: (file) =>
      run(null, async () => {
        // Loaded on use: the candidate runner shares this module and never needs it.
        const { importQti } = await import('./qti')
        const { exam, warnings } = await importQti(file)
        // The server names it; the package fills it.
        const made = await repo.createExam(exam.kind)
        const saved = await repo.saveExam({ ...exam, id: made.id, version: made.version, updatedAt: made.updatedAt }, 'draft')
        putExam(saved)
        set({
          currentExamId: saved.id,
          notice: warnings.length ? `Imported, with ${warnings.length} note${warnings.length === 1 ? '' : 's'}: ${warnings.slice(0, 3).join(' ')}${warnings.length > 3 ? ' …' : ''}` : '',
        })
        return saved
      }),

    exportExam: (id) =>
      run(undefined, async () => {
        const e = get().exams.find((x) => x.id === id)
        if (!e) return
        const { exportQti, qtiFileName } = await import('./qti')
        const url = URL.createObjectURL(await exportQti(e))
        const a = document.createElement('a')
        a.href = url
        a.download = qtiFileName(e)
        a.click()
        setTimeout(() => URL.revokeObjectURL(url), 60_000)
      }),

    setStatus: (id, status) =>
      run(null, async () => {
        const e = await repo.setStatus(id, status)
        putExam(e)
        return e
      }),

    removeExam: async (id) => {
      await get().setStatus(id, 'archived')
    },

    destroyExam: (id) =>
      run(undefined, async () => {
        await repo.deleteExam(id)
        set({ exams: get().exams.filter((e) => e.id !== id), assignments: await repo.listAssignments() })
      }),

    duplicateExam: (id) =>
      run(null, async () => {
        const copy = await repo.duplicateExam(id)
        putExam(copy)
        return copy
      }),

    loadResponses: (examId) =>
      run(undefined, async () => {
        const rs = await repo.listResponses(examId)
        set({ responses: { ...get().responses, [examId]: rs } })
      }),

    clearResponsesFor: (examId) => {
      // Locally only. Submitted work on the server is evidence and is not cleared.
      if (CORE) return
      clearResponses(examId)
      set({ responses: { ...get().responses, [examId]: [] } })
    },

    admit: (seat) => {
      let attempt: Attempt | null = null
      if (seat.attempt) {
        const a = seat.attempt
        // What the server holds, plus anything this device did that has not reached it yet.
        attempt = a.id && !a.submittedAt ? replay(a, pendingEvents(a.id)) : a
      }
      set({ seat, attempt, sessionLost: false, offline: false })
      if (attempt?.id && attempt.queued > 0) void drain(attempt.id)
      // Every figure now, while there is a network: the runner's service worker
      // keeps them, so a later outage does not blank a question.
      if (CORE)
        for (const it of allItems(seat.exam))
          for (const m of [it.image, ...it.choices.map((c) => c.image)])
            if (m?.url.startsWith('/media/')) void fetch(m.url, { credentials: 'same-origin' }).catch(() => {})
    },

    beginAttempt: async (exam, given, consents = []) => {
      const seat = get().seat
      if (CORE && seat) {
        try {
          const r = await api.candidate.startAttempt({ consents })
          const serverNow = Number(r.serverNow)
          const a = attemptIn(r.attempt!, serverNow)
          if (a.id) await openQueue(a.id)
          set({ seat: { ...seat, skew: serverNow - Date.now() } })
          set({ attempt: a.id ? replay(a, pendingEvents(a.id)) : a })
        } catch (err) {
          if (isSignedOut(err)) set({ sessionLost: true })
          else set({ notice: explain(err) })
        }
        return
      }
      // The caller's copy may be stale; what the store says now is what counts.
      const assignment = given && listAssignments().find((x) => x.id === given.id)
      // A sitting that was handed in, or whose invitation is gone, cannot be started again.
      if (given && (!assignment || assignment.status === 'submitted' || assignment.revokedAt)) return
      clearAttempt(exam.id, assignment?.id)
      const pinned = assignment?.examVersion ?? exam.publishedVersion ?? exam.version
      let a: Attempt = { ...newAttempt(exam), examVersion: pinned }
      if (assignment) {
        a = { ...a, personId: assignment.personId, assignmentId: assignment.id }
        // The evidence should say how we came to believe who this is.
        a = { ...a, events: append(a, 'identity.verified', 'personal link + code sent to the address on file') }
        // Started before, yet nothing saved on this device: the prototype keeps
        // an attempt where it was sat, so say so rather than pass it off as a first start.
        const again = assignment.status === 'started'
        if (again) a = { ...a, events: append(a, 'attempt.reentered', 'no saved copy on this device') }
        updateAssignment(assignment.id, again ? { status: 'started' } : { status: 'started', startedAt: Date.now() })
        set({ assignments: listAssignments() })
      }
      saveAttempt(a)
      set({ attempt: a })
    },

    resumeAttempt: (exam, assignmentId) => {
      // A seat on the core already carries its attempt (see admit).
      if (CORE && get().seat) return !!get().attempt && !get().attempt?.submittedAt
      const saved = getAttempt(exam.id, assignmentId)
      // Never hand one person another's paper, or leave it on screen for them.
      if (!saved || saved.submittedAt) {
        if (get().attempt && get().attempt?.assignmentId !== assignmentId) set({ attempt: null })
        return false
      }
      set({ attempt: saved })
      return true
    },

    discardAttempt: (examId, assignmentId) => {
      const a = get().attempt
      if (a?.id) forget(a.id)
      clearAttempt(examId, assignmentId)
      set({ attempt: null })
    },

    setChoice: (item, choiceId, n) => {
      const a = get().attempt
      if (!a || a.submittedAt) return
      const cur = a.answers[item.id] ?? []
      const next =
        item.type === 'multiple'
          ? cur.includes(choiceId)
            ? cur.filter((c) => c !== choiceId)
            : [...cur, choiceId]
          : [choiceId]
      record(a, { kind: 'response.changed', itemId: item.id, values: next, detail: `item ${n}` })
      commit({
        ...a,
        answers: { ...a.answers, [item.id]: next },
        queued: queuedFor(a),
        events: append(a, 'response.changed', `item ${n}`),
      })
    },

    setText: (item, value, n) => {
      const a = get().attempt
      if (!a || a.submittedAt) return
      record(a, { kind: 'response.typed', itemId: item.id, text: value, detail: `item ${n}` })
      // One event per keystroke would drown the log; coalesce consecutive edits.
      const last = a.events[a.events.length - 1]
      const repeat = last?.kind === 'response.typed' && last.detail === `item ${n}`
      commit({
        ...a,
        text: { ...a.text, [item.id]: value },
        queued: queuedFor(a),
        events: repeat
          ? [...a.events.slice(0, -1), { ...last, t: Date.now() }]
          : append(a, 'response.typed', `item ${n}`),
      })
    },

    setFile: (item, media) => {
      const a = get().attempt
      if (!a || a.submittedAt) return
      record(a, media ? { kind: 'answer.attached', itemId: item.id, file: media } : { kind: 'answer.detached', itemId: item.id })
      const files = { ...a.files }
      if (media) files[item.id] = media
      else delete files[item.id]
      commit({
        ...a,
        files,
        queued: queuedFor(a),
        events: append(a, media ? 'answer.attached' : 'answer.detached'),
      })
    },

    signal: (sig) => {
      const a = get().attempt
      if (!a || a.submittedAt) return
      if (a.id) {
        // Signals ride along with the next answers rather than forcing a send;
        // a quiet candidate's signals still go within a few seconds.
        enqueue(a.id, { ...sig, clientTime: Date.now() })
        if (!signalTimer) signalTimer = window.setTimeout(() => {
          signalTimer = 0
          void drain(a.id!)
        }, 5000)
        return
      }
      commit({ ...a, events: append(a, sig.kind, sig.detail ?? sig.values?.join(' ')) })
    },

    toggleFlag: (item, n) => {
      const a = get().attempt
      if (!a || a.submittedAt) return
      const on = a.flagged.includes(item.id)
      record(a, { kind: on ? 'item.unflagged' : 'item.flagged', itemId: item.id, detail: `item ${n}` })
      commit({
        ...a,
        flagged: on ? a.flagged.filter((f) => f !== item.id) : [...a.flagged, item.id],
        queued: a.id ? pendingCount(a.id) : a.queued,
        events: append(a, on ? 'item.unflagged' : 'item.flagged', `item ${n}`),
      })
    },

    tick: () => {
      const a = get().attempt
      if (!a || a.submittedAt) return
      if (a.deadlineAt) {
        // The server's clock, carried by the skew measured on every sync.
        const now = Date.now() + (get().seat?.skew ?? 0)
        set({ attempt: { ...a, secondsLeft: Math.max(0, Math.ceil((a.deadlineAt - now) / 1000)) } })
        return
      }
      const next = { ...a, secondsLeft: Math.max(0, a.secondsLeft - 1) }
      if (!a.id && next.secondsLeft % 10 === 0) saveAttempt(next)
      set({ attempt: next })
    },

    finalizeAttempt: async (exam, force) => {
      const a = get().attempt
      if (!a || a.submittedAt) return { ok: true }
      if (a.id) {
        // Everything answered must reach the server before it is marked.
        const r = await drain(a.id)
        if (!r.ok && r.why !== 'closed') return { ok: false, why: r.why }
        try {
          const s = await api.candidate.submitAttempt({ attemptId: a.id, force })
          if (!s.ok) return { ok: false, missing: s.missingItemIds }
          const done = attemptIn(s.attempt!, Date.now() + (get().seat?.skew ?? 0))
          forget(a.id)
          set({
            attempt: {
              ...done,
              credentialUrl: s.credentialUrl || undefined,
              events: append(a, force ? 'attempt.expired' : 'attempt.submitted', done.score ? `${done.score.pct}%` : 'response recorded'),
            },
          })
          return { ok: true }
        } catch (err) {
          if (isSignedOut(err)) return { ok: false, why: 'session' }
          return { ok: false, why: 'offline' }
        }
      }
      if (!force) {
        const missing = unanswered(exam, a)
        if (missing.length > 0) return { ok: false, missing: missing.map((m) => m.id) }
      }
      const survey = isSurvey(exam)
      const s = score(exam, a)
      const next: Attempt = {
        ...a,
        secondsLeft: force ? 0 : a.secondsLeft,
        submittedAt: Date.now(),
        score: survey ? undefined : { raw: s.raw, max: s.max, pct: s.pct, passed: s.passed },
        queued: 0,
        events: append(a, force ? 'attempt.expired' : 'attempt.submitted', survey ? 'response recorded' : `${s.pct}%`),
      }
      saveAttempt(next)
      // Against the core this is a staff preview: it is not a response.
      if (survey && !CORE) {
        addResponse(next)
        set({ responses: { ...get().responses, [exam.id]: listResponses(exam.id) } })
      }
      // Close the loop back to the invitation, so the roster shows real progress
      // rather than only "invited" forever.
      if (next.assignmentId) {
        updateAssignment(next.assignmentId, {
          status: 'submitted',
          submittedAt: Date.now(),
          scorePct: next.score?.pct,
          passed: next.score?.passed,
        })
        set({ assignments: listAssignments() })
      }
      set({ attempt: next })
      return { ok: true }
    },
  }
})

/** Responses for one instrument, fetched when first asked for and kept in sync with this session. */
export function useResponses(examId: string): Attempt[] {
  const cached = useApp((s) => s.responses[examId])
  const load = useApp((s) => s.loadResponses)
  useEffect(() => {
    if (CORE && examId && !cached) void load(examId)
  }, [examId, cached, load])
  if (cached) return cached
  return CORE || !examId ? [] : listResponses(examId)
}

/** How many have answered: submitted assignments on the core, stored responses here. */
export function useResponseCount(): (examId: string) => number {
  const assignments = useApp((s) => s.assignments)
  const byExam = useApp((s) => s.responses)
  return (examId) =>
    CORE
      ? assignments.filter((a) => a.examId === examId && a.status === 'submitted').length
      : (byExam[examId] ?? listResponses(examId)).length
}

export const useItems = (exam: Exam | null) => (exam ? allItems(exam) : [])

// A link clicked while the app is already open changes only the fragment, so
// nothing reloads. Same handling as arriving cold.
window.addEventListener('hashchange', () => {
  const token = arriveByLink()
  if (token) useApp.setState({ inviteToken: token, screen: 'player', candidateView: true })
})
