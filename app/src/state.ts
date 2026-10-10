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
  assignPeople,
  assignmentsFor,
  backfillVersions,
  dropVersions,
  ensureDrafts,
  ensurePeople,
  snapshotVersion,
  deletePerson,
  importPeople,
  listAssignments,
  listPeople,
  savePerson,
  unassign,
  updateAssignment,
  append,
  blankExam,
  clearAttempt,
  clearResponses,
  deleteExam,
  getAttempt,
  isSurvey,
  listExams,
  listResponses,
  newAttempt,
  saveAttempt,
  saveExam,
  score,
  uid,
  unanswered,
} from './store'
import { endSession, revokeInvite, sendInvites, tokenFromHash } from './invite'

/**
 * Shared application state.
 *
 * `store.ts` is the persistence layer (localStorage today, Connect RPC later).
 * This is the in-memory view of it that every screen reads.
 *
 * Two reasons it exists, stated honestly:
 *
 * 1. Every screen used to take its own `useState(() => listExams())` snapshot.
 *    That is latent staleness rather than a live bug — the shell renders one
 *    screen at a time, so navigating remounts and re-reads — but it breaks the
 *    moment two views coexist (a split pane, a modal over a list, routing that
 *    keeps screens alive). One source of truth removes the trap in advance.
 *
 * 2. Side effects belong in these actions. Zustand actions run outside React's
 *    render machinery, so unlike a `setState` updater they are NOT double-
 *    invoked under StrictMode — which is exactly how a questionnaire response
 *    once got recorded twice here. Persisting from an action is structurally
 *    safe in a way that persisting from an updater never was.
 */
type AppState = {
  /* ---- navigation ----
     Which screen is active lives here, not in the shell, so a screen can send
     you somewhere else — "open this exam in the editor" is a navigation event
     plus a selection, and both belong in one place. */
  screen: string
  setScreen: (key: string) => void
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
  reload: () => void
  upsertExam: (e: Exam) => void
  /**
   * 'same'  — overwrite the version candidates are sitting on.
   * 'new'   — branch the working copy to version + 1, leaving the live one alone.
   */
  saveVersioned: (e: Exam, mode: 'same' | 'new') => Exam
  removeExam: (id: string) => Exam[]
  createExam: (kind: Kind) => Exam
  duplicateExam: (id: string) => Exam | null
  setStatus: (id: string, status: Status) => void
  /** Hard delete. Only reachable from the archive, never from the main list. */
  destroyExam: (id: string) => Exam[]
  clearResponsesFor: (examId: string) => void

  /* ---- people and assignment ---- */
  people: Person[]
  assignments: Assignment[]
  addPerson: (name: string, email: string, group: string) => void
  importRoster: (text: string) => { added: number; skipped: number }
  /** Rows already parsed and validated from a file. Returns how many were new. */
  addPeople: (drafts: PersonDraft[]) => number
  removePerson: (id: string) => void
  /**
   * Assign, and email each new assignee their link. `sent` is 0 for a draft:
   * an invitation to something that cannot be opened is held, not mailed.
   */
  assign: (examId: string, personIds: string[], dueAt?: number) => Promise<Invited>
  /** Invite addresses that may not be on the roster; new ones are added to it. */
  assignByEmail: (examId: string, text: string, dueAt?: number) => Promise<Invited>
  /** The same, for people read from a file. */
  assignDrafts: (examId: string, drafts: PersonDraft[], dueAt?: number) => Promise<Invited>
  unassignOne: (id: string) => void
  /** Mint and mail a new link, retiring the old one. Returns how many went out. */
  sendInvite: (assignmentIds: string[]) => Promise<number>
  revokeInviteFor: (assignmentId: string) => void
  /** Re-read people and assignments after the link layer has written to them. */
  syncDelivery: () => void
  /**
   * The token from an invitation URL, held in memory for the moment between
   * arriving and being checked. It is never persisted, and it is taken out of
   * the address bar as soon as it is read.
   */
  inviteToken: string | null
  takeInviteToken: () => string | null

  /* ---- the live attempt ---- */
  attempt: Attempt | null
  beginAttempt: (exam: Exam, assignment?: Assignment) => void
  resumeAttempt: (exam: Exam, assignmentId?: string) => boolean
  discardAttempt: (examId: string, assignmentId?: string) => void
  setChoice: (item: Item, choiceId: string, n: number) => void
  setText: (item: Item, value: string, n: number) => void
  setFile: (item: Item, media: Media | undefined) => void
  toggleFlag: (item: Item, n: number) => void
  tick: () => void
  /** Returns the ids of unanswered required questions; empty means submitted. */
  finalizeAttempt: (exam: Exam, force: boolean) => string[]
}

export type Invited = { invited: number; created: number; sent: number }

/** Where a candidate's tab sits once their link is redeemed. Carries no secret. */
export const SESSION_HASH = '#/session'

/** Read an invitation token out of the address bar and take it straight back out. */
function arriveByLink(): string | null {
  const token = tokenFromHash(location.hash)
  if (token) history.replaceState(null, '', location.pathname + location.search)
  return token
}

const commit = (set: (p: Partial<AppState>) => void, next: Attempt) => {
  saveAttempt(next)
  set({ attempt: next })
}

const bootExams = listExams()
backfillVersions(bootExams)
const bootToken = arriveByLink()

export const useApp = create<AppState>((set, get) => ({
  // A candidate arrives on the candidate app, by link or by reloading mid-exam.
  screen: bootToken || location.hash === SESSION_HASH ? 'player' : 'library',
  currentExamId: null,
  previewExamId: null,

  setScreen: (key) => {
    // Leaving the candidate app is leaving the candidate's session behind.
    if (key !== 'player' && location.hash === SESSION_HASH) {
      endSession()
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

  exams: bootExams,
  responses: {},
  attempt: null,
  people: listPeople(),
  assignments: listAssignments(),
  inviteToken: bootToken,

  addPerson: (name, email, group) =>
    set({
      people: savePerson({ id: uid('p'), name, email, group, createdAt: Date.now() }),
    }),

  importRoster: (text) => {
    const { added, skipped } = importPeople(text)
    if (added.length) set({ people: listPeople() })
    return { added: added.length, skipped }
  },

  addPeople: (drafts) => {
    const { created } = ensureDrafts(drafts)
    if (created) set({ people: listPeople() })
    return created
  },

  removePerson: (id) => set({ people: deletePerson(id), assignments: listAssignments() }),

  assign: async (examId, personIds, dueAt) => {
    const e = get().exams.find((x) => x.id === examId)
    const fresh = assignPeople(examId, personIds, dueAt, e?.publishedVersion ?? e?.version ?? 1)
    const sent = await sendInvites(fresh.map((a) => a.id))
    set({ people: listPeople(), assignments: listAssignments() })
    return { invited: fresh.length, created: 0, sent }
  },

  assignByEmail: async (examId, text, dueAt) => {
    const { ids, created } = ensurePeople(text)
    return { ...(await get().assign(examId, ids, dueAt)), created }
  },

  assignDrafts: async (examId, drafts, dueAt) => {
    const { ids, created } = ensureDrafts(drafts)
    return { ...(await get().assign(examId, ids, dueAt)), created }
  },

  unassignOne: (id) => {
    unassign(id)
    set({ assignments: listAssignments() })
  },

  sendInvite: async (assignmentIds) => {
    const n = await sendInvites(assignmentIds)
    set({ assignments: listAssignments() })
    return n
  },

  revokeInviteFor: (assignmentId) => {
    revokeInvite(assignmentId)
    set({ assignments: listAssignments() })
  },

  syncDelivery: () => set({ people: listPeople(), assignments: listAssignments() }),

  takeInviteToken: () => {
    const t = get().inviteToken
    if (t) set({ inviteToken: null })
    return t
  },

  reload: () => set({ exams: listExams() }),

  upsertExam: (e) => set({ exams: saveExam(e) }),

  saveVersioned: (e, mode) => {
    const next: Exam = mode === 'new' ? { ...e, version: e.version + 1 } : e
    set({ exams: saveExam(next) })
    // Overwriting the live version must also replace its frozen copy, or the
    // snapshot and the thing candidates get would silently disagree.
    if (mode === 'same' && next.publishedVersion === next.version) snapshotVersion(next, 'updated in place')
    return next
  },

  /** Archiving is the safe default; `destroyExam` is the irreversible one. */
  removeExam: (id) => {
    const src = get().exams.find((e) => e.id === id)
    if (src) set({ exams: saveExam({ ...src, status: 'archived' }) })
    return get().exams
  },

  createExam: (kind) => {
    const e = blankExam(kind)
    set({ exams: saveExam(e), currentExamId: e.id })
    return e
  },

  setStatus: (id, status) => {
    const src = get().exams.find((e) => e.id === id)
    if (!src) return
    if (status === 'published') {
      // Publishing is what freezes a version — that is the whole point of it.
      const next: Exam = { ...src, status, publishedVersion: src.version }
      snapshotVersion(next, 'published')
      set({ exams: saveExam(next) })
      return
    }
    set({ exams: saveExam({ ...src, status }) })
  },

  destroyExam: (id) => {
    const exams = deleteExam(id)
    dropVersions(id)
    assignmentsFor(id).forEach((a) => unassign(a.id))
    set({ exams, assignments: listAssignments() })
    return exams
  },

  duplicateExam: (id) => {
    const src = get().exams.find((e) => e.id === id)
    if (!src) return null
    // New ids throughout, or the copy would share answer keys by reference.
    const copy: Exam = {
      ...src,
      id: uid('e'),
      title: `${src.title} (salinan)`,
      status: 'draft',
      updatedAt: Date.now(),
      sections: src.sections.map((sec) => ({
        ...sec,
        id: uid('s'),
        items: sec.items.map((it) => {
          const remap = new Map(it.choices.map((c) => [c.id, uid('c')]))
          return {
            ...it,
            id: uid('i'),
            choices: it.choices.map((c) => ({ ...c, id: remap.get(c.id)! })),
            correct: it.choices.length ? it.correct.map((c) => remap.get(c) ?? c) : [...it.correct],
          }
        }),
      })),
    }
    set({ exams: saveExam(copy) })
    return copy
  },

  clearResponsesFor: (examId) => {
    clearResponses(examId)
    set({ responses: { ...get().responses, [examId]: [] } })
  },

  beginAttempt: (exam, given) => {
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
    }
    saveAttempt(a)
    if (assignment) set({ assignments: listAssignments() })
    set({ attempt: a })
  },

  resumeAttempt: (exam, assignmentId) => {
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
    commit(set, {
      ...a,
      answers: { ...a.answers, [item.id]: next },
      queued: a.queued + 1,
      events: append(a, 'response.changed', `item ${n}`),
    })
  },

  setText: (item, value, n) => {
    const a = get().attempt
    if (!a || a.submittedAt) return
    // One event per keystroke would drown the log; coalesce consecutive edits.
    const last = a.events[a.events.length - 1]
    const repeat = last?.kind === 'response.typed' && last.detail === `item ${n}`
    commit(set, {
      ...a,
      text: { ...a.text, [item.id]: value },
      queued: a.queued + 1,
      events: repeat
        ? [...a.events.slice(0, -1), { ...last, t: Date.now() }]
        : append(a, 'response.typed', `item ${n}`),
    })
  },

  setFile: (item, media) => {
    const a = get().attempt
    if (!a || a.submittedAt) return
    const files = { ...a.files }
    if (media) files[item.id] = media
    else delete files[item.id]
    commit(set, {
      ...a,
      files,
      queued: a.queued + 1,
      events: append(a, media ? 'answer.attached' : 'answer.detached'),
    })
  },

  toggleFlag: (item, n) => {
    const a = get().attempt
    if (!a || a.submittedAt) return
    const on = a.flagged.includes(item.id)
    commit(set, {
      ...a,
      flagged: on ? a.flagged.filter((f) => f !== item.id) : [...a.flagged, item.id],
      events: append(a, on ? 'item.unflagged' : 'item.flagged', `item ${n}`),
    })
  },

  tick: () => {
    const a = get().attempt
    if (!a || a.submittedAt) return
    const next = { ...a, secondsLeft: Math.max(0, a.secondsLeft - 1) }
    if (next.secondsLeft % 10 === 0) saveAttempt(next)
    set({ attempt: next })
  },

  finalizeAttempt: (exam, force) => {
    const a = get().attempt
    if (!a || a.submittedAt) return []
    if (!force) {
      const missing = unanswered(exam, a)
      if (missing.length > 0) return missing.map((m) => m.id)
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
    if (survey) {
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
    return []
  },
}))

/** Responses for one instrument, kept in sync with submissions made this session. */
export function useResponses(examId: string): Attempt[] {
  const cached = useApp((s) => s.responses[examId])
  return cached ?? listResponses(examId)
}

export const useItems = (exam: Exam | null) => (exam ? allItems(exam) : [])

// A link clicked while the app is already open changes only the fragment, so
// nothing reloads. Same handling as arriving cold.
window.addEventListener('hashchange', () => {
  const token = arriveByLink()
  if (token) useApp.setState({ inviteToken: token, screen: 'player' })
})
