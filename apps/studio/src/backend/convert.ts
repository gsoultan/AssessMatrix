/**
 * The edge between the wire and the screens. The screens speak the shapes in
 * store.ts; the core speaks assessmatrix.v1. Everything that crosses goes
 * through here, so a renamed field or a new enum value breaks one file.
 *
 * protobuf-es reads int64 as bigint. Every int64 in the schema is a time in
 * Unix milliseconds or a small count, so Number() is exact for all of them.
 */
import type { MessageInitShape } from '@bufbuild/protobuf'
import * as ex from '@assessmatrix/proto/exam_pb'
import * as pe from '@assessmatrix/proto/people_pb'
import * as as from '@assessmatrix/proto/assignment_pb'
import * as ca from '@assessmatrix/proto/candidate_pb'
import * as re from '@assessmatrix/proto/result_pb'
import type {
  Assignment,
  AssignStatus,
  Attempt,
  Exam,
  ExamVersion,
  InviteEvent,
  Item,
  ItemType,
  Kind,
  Person,
  Status,
} from '../store'
import type { Media } from '../media'
import type { LinkState, Refusal } from '../invite'

const n = (v: bigint | number | undefined) => Number(v ?? 0)
/** 0 on the wire means "not set". */
const opt = (v: bigint | number | undefined) => (n(v) === 0 ? undefined : n(v))

/* ── enums ────────────────────────────────────────────────────────────────── */

const KIND: [Kind, ex.Kind][] = [
  ['exam', ex.Kind.EXAM],
  ['quiz', ex.Kind.QUIZ],
  ['survey', ex.Kind.SURVEY],
]
const STATUS: [Status, ex.Status][] = [
  ['draft', ex.Status.DRAFT],
  ['published', ex.Status.PUBLISHED],
  ['archived', ex.Status.ARCHIVED],
]
const ITEM: [ItemType, ex.ItemType][] = [
  ['single', ex.ItemType.SINGLE],
  ['multiple', ex.ItemType.MULTIPLE],
  ['boolean', ex.ItemType.BOOLEAN],
  ['short', ex.ItemType.SHORT],
  ['essay', ex.ItemType.ESSAY],
  ['likert', ex.ItemType.LIKERT],
  ['rating', ex.ItemType.RATING],
]
const ASSIGN: [AssignStatus, as.AssignStatus][] = [
  ['invited', as.AssignStatus.INVITED],
  ['started', as.AssignStatus.STARTED],
  ['submitted', as.AssignStatus.SUBMITTED],
  ['expired', as.AssignStatus.EXPIRED],
]
const LINK: [LinkState, as.LinkState][] = [
  ['unsent', as.LinkState.UNSENT],
  ['live', as.LinkState.LIVE],
  ['used', as.LinkState.USED],
  ['expired', as.LinkState.EXPIRED],
  ['locked', as.LinkState.LOCKED],
  ['revoked', as.LinkState.REVOKED],
]

function table<A, B>(pairs: [A, B][], fallbackA: A) {
  return {
    out: (a: A) => pairs.find((p) => p[0] === a)?.[1] ?? (0 as B),
    in: (b: B) => pairs.find((p) => p[1] === b)?.[0] ?? fallbackA,
  }
}

export const kind = table(KIND, 'exam')
export const status = table(STATUS, 'draft')
const itemType = table(ITEM, 'single')
const assignStatus = table(ASSIGN, 'invited')
const linkState = table(LINK, 'unsent')

/** Refusals the candidate screens know how to explain. */
export type WireRefusal = Refusal | 'cooldown' | 'limit' | 'wrong' | 'stale'

export function refusal(r: ca.Refusal): WireRefusal {
  switch (r) {
    case ca.Refusal.REVOKED: return 'revoked'
    case ca.Refusal.EXPIRED: return 'expired'
    case ca.Refusal.LOCKED: return 'locked'
    case ca.Refusal.USED: return 'used'
    case ca.Refusal.SUPERSEDED: return 'superseded'
    case ca.Refusal.SUBMITTED: return 'submitted'
    case ca.Refusal.CLOSED: return 'closed'
    case ca.Refusal.COOLDOWN: return 'cooldown'
    case ca.Refusal.LIMIT: return 'limit'
    case ca.Refusal.WRONG_CODE: return 'wrong'
    case ca.Refusal.STALE_CODE: return 'stale'
    default: return 'unknown'
  }
}

/* ── instruments ──────────────────────────────────────────────────────────── */

export const mediaIn = (m: ex.Media | undefined): Media | undefined =>
  m && m.url ? { id: m.id, url: m.url, alt: m.alt, w: m.w, h: m.h, bytes: n(m.bytes) } : undefined

const mediaOut = (m: Media | undefined): MessageInitShape<typeof ex.MediaSchema> | undefined =>
  m ? { id: m.id, url: m.url, alt: m.alt, w: m.w, h: m.h, bytes: BigInt(Math.round(m.bytes)) } : undefined

function itemIn(i: ex.Item): Item {
  const out: Item = {
    id: i.id,
    type: itemType.in(i.type),
    stem: i.stem,
    choices: i.choices.map((c) => ({ id: c.id, text: c.text, image: mediaIn(c.image) })),
    correct: [...i.correct],
    points: i.points,
    skill: i.skill,
  }
  const image = mediaIn(i.image)
  if (image) out.image = image
  if (i.allowAnswerImage) out.allowAnswerImage = true
  if (i.required) out.required = true
  if (i.scale) out.scale = { points: i.scale.points, minLabel: i.scale.minLabel, maxLabel: i.scale.maxLabel }
  return out
}

export function examIn(e: ex.Exam): Exam {
  return {
    id: e.id,
    kind: kind.in(e.kind),
    status: status.in(e.status),
    version: e.version,
    publishedVersion: e.publishedVersion || undefined,
    title: e.title,
    issuer: e.issuer,
    tier: Math.min(3, Math.max(0, e.tier)) as Exam['tier'],
    passPct: e.passPct,
    shuffle: e.shuffle,
    timed: e.timed,
    anonymous: e.anonymous,
    sections: e.sections.map((s) => ({ id: s.id, name: s.name, minutes: s.minutes, items: s.items.map(itemIn) })),
    updatedAt: n(e.updatedAt),
    ...(e.sharedFrom
      ? { sharedFrom: { examId: e.sharedFrom.examId, version: e.sharedFrom.version, tenantName: e.sharedFrom.tenantName } }
      : {}),
  }
}

export function examOut(e: Exam): MessageInitShape<typeof ex.ExamSchema> {
  return {
    id: e.id,
    kind: kind.out(e.kind),
    status: status.out(e.status),
    version: e.version,
    publishedVersion: e.publishedVersion ?? 0,
    title: e.title,
    issuer: e.issuer,
    tier: e.tier,
    passPct: e.passPct,
    shuffle: e.shuffle,
    timed: e.timed,
    anonymous: e.anonymous,
    updatedAt: BigInt(e.updatedAt),
    sections: e.sections.map((s) => ({
      id: s.id,
      name: s.name,
      minutes: s.minutes,
      items: s.items.map((i) => ({
        id: i.id,
        type: itemType.out(i.type),
        stem: i.stem,
        image: mediaOut(i.image),
        choices: i.choices.map((c) => ({ id: c.id, text: c.text, image: mediaOut(c.image) })),
        correct: i.correct,
        points: i.points,
        skill: i.skill,
        allowAnswerImage: !!i.allowAnswerImage,
        required: !!i.required,
        scale: i.scale,
      })),
    })),
  }
}

export const versionIn = (v: ex.ExamVersion): ExamVersion => ({
  examId: v.examId,
  version: v.version,
  publishedAt: n(v.publishedAt),
  note: v.note,
  exam: v.exam ? examIn(v.exam) : (undefined as unknown as Exam),
})

/* ── people and assignments ───────────────────────────────────────────────── */

export const personIn = (p: pe.Person): Person => ({
  id: p.id,
  name: p.name,
  email: p.email,
  group: p.group,
  createdAt: n(p.createdAt),
})

const inviteEventIn = (e: as.InviteEvent): InviteEvent => ({
  seq: e.seq,
  t: n(e.at),
  kind: e.kind,
  // The server counts repeats; the screens read them the way the prototype wrote them.
  detail: e.count > 1 ? `${e.detail} ×${e.count}`.trim() : e.detail || undefined,
})

/**
 * The link's secrets never leave the server, so the fields that held their
 * hashes in the prototype stay empty. `linkState` is the server's verdict.
 */
export const assignmentIn = (a: as.Assignment): Assignment => ({
  id: a.id,
  examId: a.examId,
  examVersion: a.examVersion,
  personId: a.personId,
  sitting: a.sitting || 1,
  assignedAt: n(a.assignedAt),
  dueAt: opt(a.dueAt),
  status: assignStatus.in(a.status),
  startedAt: opt(a.startedAt),
  submittedAt: opt(a.submittedAt),
  scorePct: a.scorePct,
  passed: a.passed,
  tokenHash: '',
  retiredHashes: [],
  sentAt: opt(a.sentAt),
  expiresAt: opt(a.expiresAt),
  redeemedAt: opt(a.redeemedAt),
  revokedAt: opt(a.revokedAt),
  otpSends: a.otpSends,
  selfServes: a.selfServes,
  history: a.history.map(inviteEventIn),
  linkState: linkState.in(a.linkState),
})

/* ── attempts ─────────────────────────────────────────────────────────────── */

type Answers = { [k: string]: ca.ItemAnswer }

function answersIn(answers: Answers) {
  const out = { answers: {} as Attempt['answers'], text: {} as Attempt['text'], files: {} as Attempt['files'] }
  for (const [id, a] of Object.entries(answers)) {
    if (a.values.length) out.answers[id] = [...a.values]
    if (a.text) out.text[id] = a.text
    const f = mediaIn(a.file)
    if (f) out.files[id] = f
  }
  return out
}

const scoreIn = (s: ca.Score | undefined) =>
  s ? { raw: s.raw, max: s.max, pct: s.pct, passed: s.passed } : undefined

/**
 * A server attempt as the candidate screens hold one. `secondsLeft` is worked
 * out from the server's deadline and the server's clock, never the device's.
 */
export function attemptIn(a: ca.Attempt, serverNow: number): Attempt {
  const deadlineAt = opt(a.deadlineAt)
  return {
    id: a.id,
    examId: a.examId,
    examVersion: a.examVersion,
    assignmentId: a.assignmentId,
    ...answersIn(a.answers),
    flagged: [...a.flagged],
    startedAt: n(a.startedAt),
    deadlineAt,
    secondsLeft: deadlineAt ? Math.max(0, Math.ceil((deadlineAt - serverNow) / 1000)) : 0,
    submittedAt: opt(a.submittedAt),
    score: scoreIn(a.score),
    events: a.events.map((e) => ({ seq: n(e.seq), t: n(e.serverTime), kind: e.kind, detail: e.detail || undefined })),
    queued: 0,
  }
}

/** A submitted response, in the shape the Responses screen aggregates. */
export const responseIn = (r: re.Response): Attempt => ({
  id: r.attemptId || undefined,
  examId: '',
  examVersion: r.examVersion,
  personId: r.personId || undefined,
  assignmentId: r.assignmentId || undefined,
  ...answersIn(r.answers),
  flagged: [],
  startedAt: 0,
  secondsLeft: 0,
  submittedAt: n(r.submittedAt),
  score: scoreIn(r.score),
  events: [],
  queued: 0,
})

export type OutEvent = MessageInitShape<typeof ca.AttemptEventSchema>
export { mediaOut }
