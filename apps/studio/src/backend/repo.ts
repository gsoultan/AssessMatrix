/**
 * Everything a staff screen reads or writes, as one async interface with two
 * implementations: this browser (the prototype) and the Go core.
 *
 * state.ts is the only caller. Screens never import this file, so which
 * backend a build uses is invisible to them.
 */
import { ConnectError, Code } from '@connectrpc/connect'
import { api } from '../api/client'
import * as store from '../store'
import type { Assignment, Attempt, Exam, ExamVersion, Kind, Person, PersonDraft, Status } from '../store'
import { revokeInvite, sendInvites } from '../invite'
import { SaveMode } from '@assessmatrix/proto/exam_pb'
import { CORE } from './mode'
import { assignmentIn, examIn, examOut, kind, personIn, responseIn, status, versionIn } from './convert'

/** 'draft' saves a working copy; 'same' rewrites the live version; 'new' branches to version + 1. */
export type SaveHow = 'draft' | 'same' | 'new'

export type Repo = {
  load(): Promise<{ exams: Exam[]; people: Person[]; assignments: Assignment[] }>
  listExams(): Promise<Exam[]>
  createExam(kind: Kind): Promise<Exam>
  saveExam(e: Exam, how: SaveHow): Promise<Exam>
  setStatus(id: string, s: Status): Promise<Exam>
  duplicateExam(id: string): Promise<Exam>
  /** Permanent. The core refuses unless the instrument is archived and was never sat. */
  deleteExam(id: string): Promise<void>
  listVersions(examId: string): Promise<ExamVersion[]>

  listPeople(): Promise<Person[]>
  /** Everyone in the list ends up on the roster once; ids come back in list order. */
  ensurePeople(drafts: PersonDraft[]): Promise<{ ids: string[]; created: number }>
  deletePerson(id: string): Promise<void>

  listAssignments(): Promise<Assignment[]>
  /** Assign and mail each new assignee. `sent` is 0 for a draft. */
  assign(examId: string, personIds: string[], dueAt?: number): Promise<{ invited: number; sent: number }>
  unassign(id: string): Promise<void>
  sendInvites(ids: string[]): Promise<number>
  revokeInvite(id: string): Promise<void>

  listResponses(examId: string): Promise<Attempt[]>
}

/* ── this browser ─────────────────────────────────────────────────────────── */

const find = (id: string) => store.listExams().find((e) => e.id === id)

const local: Repo = {
  async load() {
    const exams = store.listExams()
    store.backfillVersions(exams)
    return { exams, people: store.listPeople(), assignments: store.listAssignments() }
  },
  async listExams() {
    return store.listExams()
  },
  async createExam(k) {
    const e = store.blankExam(k)
    store.saveExam(e)
    return find(e.id)!
  },
  async saveExam(e, how) {
    const next: Exam = how === 'new' ? { ...e, version: e.version + 1 } : e
    store.saveExam(next)
    const saved = find(e.id)!
    // Overwriting the live version must also replace its frozen copy, or the
    // snapshot and the thing candidates get would silently disagree.
    if (how === 'same' && saved.publishedVersion === saved.version) store.snapshotVersion(saved, 'updated in place')
    return saved
  },
  async setStatus(id, s) {
    const src = find(id)
    if (!src) throw new Error('not found')
    if (s === 'published') {
      // Publishing is what freezes a version — that is the whole point of it.
      const next: Exam = { ...src, status: s, publishedVersion: src.version }
      store.snapshotVersion(next, 'published')
      store.saveExam(next)
    } else store.saveExam({ ...src, status: s })
    return find(id)!
  },
  async duplicateExam(id) {
    const src = find(id)
    if (!src) throw new Error('not found')
    const uid = store.uid
    // New ids throughout, or the copy would share answer keys by reference.
    const copy: Exam = {
      ...src,
      id: uid('e'),
      title: `${src.title} (salinan)`,
      status: 'draft',
      version: 1,
      publishedVersion: undefined,
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
    store.saveExam(copy)
    return find(copy.id)!
  },
  async deleteExam(id) {
    store.deleteExam(id)
    store.dropVersions(id)
    store.assignmentsFor(id).forEach((a) => store.unassign(a.id))
  },
  async listVersions(examId) {
    return store.listVersions(examId)
  },
  async listPeople() {
    return store.listPeople()
  },
  async ensurePeople(drafts) {
    return store.ensureDrafts(drafts)
  },
  async deletePerson(id) {
    store.deletePerson(id)
  },
  async listAssignments() {
    return store.listAssignments()
  },
  async assign(examId, personIds, dueAt) {
    const e = find(examId)
    const fresh = store.assignPeople(examId, personIds, dueAt, e?.publishedVersion ?? e?.version ?? 1)
    const sent = await sendInvites(fresh.map((a) => a.id))
    return { invited: fresh.length, sent }
  },
  async unassign(id) {
    store.unassign(id)
  },
  async sendInvites(ids) {
    return sendInvites(ids)
  },
  async revokeInvite(id) {
    revokeInvite(id)
  },
  async listResponses(examId) {
    return store.listResponses(examId)
  },
}

/* ── the Go core ──────────────────────────────────────────────────────────── */

const SAVE: Record<SaveHow, SaveMode> = {
  draft: SaveMode.DRAFT,
  same: SaveMode.IN_PLACE,
  new: SaveMode.NEW_VERSION,
}

const core: Repo = {
  async load() {
    const [exams, people, assignments] = await Promise.all([core.listExams(), core.listPeople(), core.listAssignments()])
    return { exams, people, assignments }
  },
  async listExams() {
    return (await api.exams.listExams({})).exams.map(examIn)
  },
  async createExam(k) {
    return examIn((await api.exams.createExam({ kind: kind.out(k) })).exam!)
  },
  async saveExam(e, how) {
    // The server refuses the save if someone else saved since this copy was read.
    const r = await api.exams.saveExam({ exam: examOut(e), mode: SAVE[how], expectedUpdatedAt: BigInt(e.updatedAt) })
    return examIn(r.exam!)
  },
  async setStatus(id, s) {
    return examIn((await api.exams.setExamStatus({ id, status: status.out(s) })).exam!)
  },
  async duplicateExam(id) {
    return examIn((await api.exams.duplicateExam({ id })).exam!)
  },
  async deleteExam(id) {
    await api.exams.deleteExam({ id })
  },
  async listVersions(examId) {
    return (await api.exams.listExamVersions({ examId })).versions.map(versionIn)
  },
  async listPeople() {
    return (await api.people.listPeople({})).people.map(personIn)
  },
  async ensurePeople(drafts) {
    if (drafts.length === 0) return { ids: [], created: 0 }
    const r = await api.people.ensurePeople({ people: drafts })
    return { ids: r.ids, created: r.created }
  },
  async deletePerson(id) {
    await api.people.deletePerson({ id })
  },
  async listAssignments() {
    return (await api.assignments.listAssignments({})).assignments.map(assignmentIn)
  },
  async assign(examId, personIds, dueAt) {
    if (personIds.length === 0) return { invited: 0, sent: 0 }
    const r = await api.assignments.assignPeople({ examId, personIds, dueAt: BigInt(dueAt ?? 0) })
    return { invited: r.assignments.length, sent: r.sent }
  },
  async unassign(id) {
    await api.assignments.unassign({ id })
  },
  async sendInvites(ids) {
    if (ids.length === 0) return 0
    return (await api.assignments.sendInvites({ assignmentIds: ids })).sent
  },
  async revokeInvite(id) {
    await api.assignments.revokeInvite({ assignmentId: id })
  },
  async listResponses(examId) {
    return (await api.results.listResponses({ examId })).responses.map((r) => ({ ...responseIn(r), examId }))
  },
}

export const repo: Repo = CORE ? core : local

/** Words a staff member can act on, from whatever a call threw. */
export function explain(err: unknown): string {
  if (err instanceof store.StorageFullError) return err.message
  if (err instanceof ConnectError) {
    switch (err.code) {
      case Code.Unauthenticated:
        return 'Your session has ended. Sign in again.'
      case Code.Aborted:
        return 'Someone else changed this since you opened it. Reload to see their version before saving.'
      case Code.InvalidArgument:
      case Code.FailedPrecondition:
        return err.rawMessage
      case Code.Unavailable:
        return 'Cannot reach the server. Check your connection and try again.'
      default:
        return 'Something went wrong on the server. Try again; if it keeps happening, tell your administrator.'
    }
  }
  return err instanceof Error ? err.message : 'Something went wrong.'
}

export const isSignedOut = (err: unknown) => err instanceof ConnectError && err.code === Code.Unauthenticated
