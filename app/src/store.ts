/**
 * Local persistence for the prototype. localStorage stands in for the Go core —
 * the shapes here deliberately mirror what the real API would return, and an
 * attempt is an append-only event log rather than a mutable row, exactly as in
 * docs/03-architecture.md. Swapping this file for Connect RPC calls should not
 * require touching a screen.
 */

export type ItemType = 'single' | 'multiple' | 'boolean' | 'short' | 'essay' | 'likert' | 'rating'

/**
 * What this instrument is *for*. The engine underneath is identical — sections,
 * delivery, autosave, media, event log. What changes is whether answers have a
 * right one, whether there is a clock, and what "results" means.
 */
export type Kind = 'exam' | 'quiz' | 'survey'

/**
 * Lifecycle. A draft is invisible to candidates; archived is retired but
 * recoverable. Deleting is deliberately not the default action — an instrument
 * carries authored work and, once it has been sat, evidence that someone may
 * need to produce later.
 */
export type Status = 'draft' | 'published' | 'archived'

export const STATUSES: { v: Status; label: string; hint: string }[] = [
  { v: 'draft', label: 'Draft', hint: 'Only you can see it. Candidates cannot start it.' },
  { v: 'published', label: 'Published', hint: 'Live. Candidates can start it.' },
  { v: 'archived', label: 'Archived', hint: 'Retired and hidden, but recoverable.' },
]

export const KINDS = [
  {
    v: 'exam' as const,
    label: 'Exam',
    hint: 'Graded and timed, with integrity controls. Produces a pass or a fail.',
  },
  {
    v: 'quiz' as const,
    label: 'Quiz',
    hint: 'Graded but light. No proctoring, no clock unless you add one.',
  },
  {
    v: 'survey' as const,
    label: 'Questionnaire',
    hint: 'Ungraded. No correct answers — collect responses and read the distribution.',
  },
]

export type { Media } from './media'
import type { Media } from './media'

export type Choice = { id: string; text: string; image?: Media }

export type Item = {
  id: string
  type: ItemType
  stem: string
  /** Figure shown with the question — diagram, chart, screenshot. */
  image?: Media
  choices: Choice[]
  correct: string[]
  points: number
  skill: string
  /** Let the participant attach an image to a short/essay answer. */
  allowAnswerImage?: boolean
  /** Questionnaires need this far more than exams do. */
  required?: boolean
  /** likert / rating: how many steps, and what the ends mean. */
  scale?: { points: number; minLabel: string; maxLabel: string }
}

export type Section = { id: string; name: string; minutes: number; items: Item[] }

export type Exam = {
  id: string
  kind: Kind
  status: Status
  /** The working version being edited. Starts at 1. */
  version: number
  /** Which version candidates actually get. Undefined until first published. */
  publishedVersion?: number
  title: string
  issuer: string
  tier: 0 | 1 | 2 | 3
  passPct: number
  shuffle: boolean
  timed: boolean
  /** Surveys usually want this; exams almost never do. */
  anonymous: boolean
  sections: Section[]
  updatedAt: number
}

export const isLive = (e: Exam) => e.status === 'published'
/** True when the working copy has moved ahead of what candidates are sitting. */
export const hasUnpublishedVersion = (e: Exam) =>
  e.publishedVersion !== undefined && e.version > e.publishedVersion

/** A questionnaire has no right answers, so nothing about it is scored. */
export const isScored = (e: Exam) => e.kind !== 'survey'
export const isSurvey = (e: Exam) => e.kind === 'survey'

/* ── people and assignment ─────────────────────────────────────────────────
   An instrument is not delivered by publishing it — it is delivered by giving
   named people a way in. That link is the assignment: one person, one
   instrument, one token, one lifecycle.                                     */

export type Person = {
  id: string
  name: string
  email: string
  /** Cohort, department, class — whatever the organisation groups by. */
  group: string
  createdAt: number
}

export type AssignStatus = 'invited' | 'started' | 'submitted' | 'expired'

export const ASSIGN_STATUS: { v: AssignStatus; label: string }[] = [
  { v: 'invited', label: 'Invited' },
  { v: 'started', label: 'In progress' },
  { v: 'submitted', label: 'Submitted' },
  { v: 'expired', label: 'Expired' },
]

export type Assignment = {
  id: string
  examId: string
  /** The version this invitation opens. Pinned when assigned. */
  examVersion: number
  personId: string
  assignedAt: number
  dueAt?: number
  status: AssignStatus
  startedAt?: number
  submittedAt?: number
  scorePct?: number
  passed?: boolean

  /* The invitation link. The token itself is never stored — only its SHA-256,
     so nothing here (and no one reading it) can reproduce a working link. All
     of this is written by invite.ts and nowhere else. */
  /** Hash of the one live link token. Empty until an invitation is sent. */
  tokenHash: string
  /** Hashes of links this one replaced, so an old email can say so. Bounded. */
  retiredHashes: string[]
  sentAt?: number
  expiresAt?: number
  /** Set when the emailed code was verified. A link is spent from then on. */
  redeemedAt?: number
  revokedAt?: number
  /** Hash of the one session a redeemed link started. One device at a time. */
  sessionHash?: string
  otp?: { hash: string; expiresAt: number; tries: number; sentAt: number }
  /** Codes sent for the current link. At the cap the link locks. */
  otpSends: number
  /** New links the candidate asked for themselves since an admin last sent one. */
  selfServes: number
  history: InviteEvent[]
}

/** Append-only. What happened to an invitation, in order, for whoever has to explain it. */
export type InviteEvent = { seq: number; t: number; kind: string; detail?: string }

export type AttemptEvent = { seq: number; t: number; kind: string; detail?: string }

export type Attempt = {
  examId: string
  answers: Record<string, string[]>
  text: Record<string, string>
  /** Participant-attached images, keyed by item id. */
  files: Record<string, Media>
  flagged: string[]
  startedAt: number
  secondsLeft: number
  submittedAt?: number
  score?: { raw: number; max: number; pct: number; passed: boolean }
  events: AttemptEvent[]
  queued: number
  /** Pinned at start. A mid-attempt edit must not change what is being sat. */
  examVersion?: number
  /** Set when the attempt was reached through an invitation. */
  personId?: string
  assignmentId?: string
}

export const TIERS = [
  { v: 0, label: 'Tier 0 · Open', hint: 'Practice. No integrity controls.' },
  { v: 1, label: 'Tier 1 · Monitored', hint: 'Device fingerprint, focus & timing signals.' },
  { v: 2, label: 'Tier 2 · Proctored', hint: '+ on-device vision, lockdown, evidence timeline.' },
  { v: 3, label: 'Tier 3 · Certified', hint: '+ biometric identity, human review, chain of custody.' },
] as const

export const ITEM_TYPES: { v: ItemType; label: string; auto: boolean; survey?: boolean }[] = [
  { v: 'single', label: 'Single choice', auto: true },
  { v: 'multiple', label: 'Multiple choice', auto: true },
  { v: 'boolean', label: 'True / false', auto: true },
  { v: 'short', label: 'Short answer', auto: true },
  { v: 'essay', label: 'Essay', auto: false },
  { v: 'likert', label: 'Likert scale', auto: false, survey: true },
  { v: 'rating', label: 'Rating', auto: false, survey: true },
]

/** Scale types express an opinion; there is no key to mark them against. */
export const isScale = (t: ItemType) => t === 'likert' || t === 'rating'

export const uid = (p = 'x') => `${p}_${Math.random().toString(36).slice(2, 9)}`

const EXAMS_KEY = 'am.exams.v1'
const ATTEMPT_PREFIX = 'am.attempt.v1.'
/**
 * An invited attempt is keyed by its assignment as well as its instrument. Two
 * people sitting the same exam on one lab computer must not resume — or wipe —
 * each other's work.
 */
const attemptKey = (examId: string, assignmentId?: string) =>
  `${ATTEMPT_PREFIX}${examId}${assignmentId ? `.${assignmentId}` : ''}`

function read<T>(k: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(k)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}

export class StorageFullError extends Error {
  constructor() {
    super('Browser storage is full. Remove or replace an image, or delete an unused exam.')
  }
}

function write(k: string, v: unknown) {
  try {
    localStorage.setItem(k, JSON.stringify(v))
  } catch (e) {
    // Images made this a real failure mode rather than a shrug. In the real
    // runner this is where the OPFS queue takes over; here the caller must be
    // told, because silently losing an author's work is unforgivable.
    if (e instanceof DOMException && (e.name === 'QuotaExceededError' || e.code === 22))
      throw new StorageFullError()
    throw e
  }
}

export function blankItem(type: ItemType = 'single'): Item {
  const base = { id: uid('i'), type, stem: '', points: 1, skill: '', correct: [] as string[] }
  if (type === 'likert')
    return {
      ...base,
      choices: [],
      scale: { points: 5, minLabel: 'Sangat tidak setuju', maxLabel: 'Sangat setuju' },
    }
  if (type === 'rating')
    return { ...base, choices: [], scale: { points: 10, minLabel: 'Tidak mungkin', maxLabel: 'Sangat mungkin' } }
  if (type === 'boolean')
    return { ...base, choices: [{ id: 'true', text: 'Benar' }, { id: 'false', text: 'Salah' }] }
  if (type === 'short' || type === 'essay') return { ...base, choices: [] }
  return {
    ...base,
    choices: [
      { id: uid('c'), text: '' },
      { id: uid('c'), text: '' },
      { id: uid('c'), text: '' },
      { id: uid('c'), text: '' },
    ],
  }
}

export function blankSection(n = 1): Section {
  return { id: uid('s'), name: `Bagian ${n}`, minutes: 30, items: [blankItem()] }
}

export function blankExam(kind: Kind = 'exam'): Exam {
  const survey = kind === 'survey'
  return {
    id: uid('e'),
    kind,
    status: 'draft',
    version: 1,
    title: survey ? 'Kuesioner baru' : kind === 'quiz' ? 'Kuis baru' : 'Asesmen baru',
    issuer: 'Organisasi Anda',
    tier: kind === 'exam' ? 2 : 0,
    passPct: 70,
    shuffle: kind !== 'survey',
    timed: kind === 'exam',
    anonymous: survey,
    sections: [{ ...blankSection(), items: [blankItem(survey ? 'likert' : 'single')] }],
    updatedAt: Date.now(),
  }
}

/* A seeded exam so the builder and the player are never staring at nothing. */
function seed(): Exam {
  const c = (t: string) => ({ id: uid('c'), text: t })
  const a1 = c('Penulis memblokir, latensi permintaan naik, lalu pulih')
  const b1 = c('Baris audit lama ditimpa secara diam-diam')
  const c1 = c('Channel tumbuh dinamis; memori naik hingga OOM')
  const d1 = c('Runtime menjatuhkan goroutine penulis dengan panic')
  const a2 = c('Indeks pada kolom yang sering difilter')
  const b2 = c('Indeks pada setiap kolom tabel')
  const c2 = c('Menghapus seluruh indeks sekunder')
  const d2 = c('Menaikkan ukuran halaman menjadi 64 KB')
  const a3 = c('Membatasi ukuran dan memasang eviksi')
  const b3 = c('Menambah RAM pada node')
  const c3 = c('Menaikkan TTL cache')
  const d3 = c('Mengabaikannya; Go punya GC')

  return {
    id: 'seed_backend_l3',
    kind: 'exam',
    status: 'published',
    version: 1,
    publishedVersion: 1,
    timed: true,
    anonymous: false,
    title: 'Sertifikasi Backend Engineer — Level 3',
    issuer: 'LSP Teknologi Informasi',
    tier: 3,
    passPct: 70,
    shuffle: false,
    updatedAt: Date.now(),
    sections: [
      {
        id: uid('s'),
        name: 'Konkurensi',
        minutes: 20,
        items: [
          {
            id: uid('i'),
            type: 'single',
            skill: 'Concurrency & parallelism',
            points: 2,
            stem: 'Sebuah layanan Go menerima 12.000 permintaan per detik. Setiap permintaan menulis satu baris audit ke channel ber-buffer berkapasitas 1.024 yang dikonsumsi oleh satu goroutine tunggal. Apa yang terjadi ketika konsumen melambat menjadi 9.000 baris per detik selama 30 detik?',
            choices: [a1, b1, c1, d1],
            correct: [a1.id],
          },
          {
            id: uid('i'),
            type: 'boolean',
            skill: 'Concurrency & parallelism',
            points: 1,
            stem: 'Membaca sebuah map Go dari banyak goroutine tanpa penulis bersamaan adalah aman.',
            choices: [
              { id: 'true', text: 'Benar' },
              { id: 'false', text: 'Salah' },
            ],
            correct: ['true'],
          },
        ],
      },
      {
        id: uid('s'),
        name: 'Data & Ketahanan',
        minutes: 25,
        items: [
          {
            id: uid('i'),
            type: 'single',
            skill: 'Data modelling',
            points: 2,
            stem: 'Kueri laporan harian melambat seiring bertambahnya data. Langkah pertama yang paling tepat?',
            choices: [a2, b2, c2, d2],
            correct: [a2.id],
          },
          {
            id: uid('i'),
            type: 'multiple',
            skill: 'Security fundamentals',
            points: 3,
            stem: 'Sebuah cache in-memory dikunci oleh nilai dari path permintaan. Pilih semua mitigasi yang wajib.',
            choices: [a3, b3, c3, d3],
            correct: [a3.id],
          },
          {
            id: uid('i'),
            type: 'short',
            skill: 'Observability',
            points: 2,
            stem: 'Sebutkan satu metrik yang paling awal menunjukkan saturasi antrean tulis. (satu kata)',
            choices: [],
            correct: ['lag', 'backlog', 'depth', 'queue depth'],
          },
        ],
      },
    ],
  }
}

function seedSurvey(): Exam {
  const c = (t: string) => ({ id: uid('c'), text: t })
  const likert = (stem: string): Item => ({
    id: uid('i'),
    type: 'likert',
    stem,
    choices: [],
    correct: [],
    points: 0,
    skill: '',
    required: true,
    scale: { points: 5, minLabel: 'Sangat tidak setuju', maxLabel: 'Sangat setuju' },
  })
  const roles = [c('Rekayasa perangkat lunak'), c('Data'), c('Produk'), c('Operasi'), c('Lainnya')]

  return {
    id: 'seed_survey_onboarding',
    kind: 'survey',
    status: 'published',
    version: 1,
    publishedVersion: 1,
    title: 'Kuesioner Pengalaman Onboarding',
    issuer: 'PT Nusantara Digital',
    tier: 0,
    passPct: 0,
    shuffle: false,
    timed: false,
    anonymous: true,
    updatedAt: Date.now(),
    sections: [
      {
        id: uid('s'),
        name: 'Tentang Anda',
        minutes: 5,
        items: [
          {
            id: uid('i'),
            type: 'single',
            stem: 'Di divisi mana Anda bekerja?',
            choices: roles,
            correct: [],
            points: 0,
            skill: '',
            required: true,
          },
        ],
      },
      {
        id: uid('s'),
        name: 'Pengalaman minggu pertama',
        minutes: 5,
        items: [
          likert('Saya memahami apa yang diharapkan dari peran saya.'),
          likert('Saya mendapat perangkat dan akses tepat waktu.'),
          {
            id: uid('i'),
            type: 'rating',
            stem: 'Seberapa besar kemungkinan Anda merekomendasikan tempat ini kepada teman?',
            choices: [],
            correct: [],
            points: 0,
            skill: '',
            required: true,
            scale: { points: 10, minLabel: 'Tidak mungkin', maxLabel: 'Sangat mungkin' },
          },
          {
            id: uid('i'),
            type: 'essay',
            stem: 'Satu hal yang paling ingin Anda ubah dari proses onboarding?',
            choices: [],
            correct: [],
            points: 0,
            skill: '',
          },
        ],
      },
    ],
  }
}

/** Older stored records predate `kind`; fill the gaps rather than crash on them. */
const normalise = (e: Exam): Exam => ({
  ...e,
  kind: e.kind ?? 'exam',
  // Records written before the lifecycle existed were all effectively live.
  status: e.status ?? 'published',
  version: e.version ?? 1,
  publishedVersion: e.publishedVersion ?? (e.status ?? 'published') === 'published' ? e.version ?? 1 : undefined,
  timed: e.timed ?? true,
  anonymous: e.anonymous ?? false,
})

export function listExams(): Exam[] {
  const xs = read<Exam[]>(EXAMS_KEY, [])
  if (xs.length === 0) {
    const fresh = [seed(), seedSurvey()]
    write(EXAMS_KEY, fresh)
    return fresh
  }
  return xs.map(normalise)
}

export function saveExam(exam: Exam): Exam[] {
  const xs = listExams()
  const next = { ...exam, updatedAt: Date.now() }
  const i = xs.findIndex((e) => e.id === exam.id)
  if (i >= 0) xs[i] = next
  else xs.unshift(next)
  write(EXAMS_KEY, xs)
  return xs
}

export function deleteExam(id: string): Exam[] {
  const xs = listExams().filter((e) => e.id !== id)
  write(EXAMS_KEY, xs)
  try {
    const own = attemptKey(id)
    for (const k of Object.keys(localStorage)) if (k === own || k.startsWith(`${own}.`)) localStorage.removeItem(k)
  } catch { /* ignore */ }
  return xs
}

export const allItems = (e: Exam): Item[] => e.sections.flatMap((s) => s.items)

/** Bytes of attached media in an exam — the thing that actually fills the quota. */
export const examMediaBytes = (e: Exam) =>
  allItems(e).reduce(
    (n, i) => n + (i.image?.bytes ?? 0) + i.choices.reduce((m, c) => m + (c.image?.bytes ?? 0), 0),
    0,
  )

/** Items carrying an image with no alt text — an accessibility (and legal) defect. */
export const missingAlt = (e: Exam) =>
  allItems(e).filter(
    (i) => (i.image && !i.image.alt.trim()) || i.choices.some((c) => c.image && !c.image.alt.trim()),
  ).length
export const totalMinutes = (e: Exam) => e.sections.reduce((n, s) => n + s.minutes, 0)
export const totalPoints = (e: Exam) => allItems(e).reduce((n, i) => n + i.points, 0)

export function getAttempt(examId: string, assignmentId?: string): Attempt | null {
  return read<Attempt | null>(attemptKey(examId, assignmentId), null)
}

export function saveAttempt(a: Attempt) {
  write(attemptKey(a.examId, a.assignmentId), a)
}

export function clearAttempt(examId: string, assignmentId?: string) {
  try {
    localStorage.removeItem(attemptKey(examId, assignmentId))
  } catch { /* ignore */ }
}

/* ── versions ──────────────────────────────────────────────────────────────
   A published version is immutable by intent: an attempt is pinned to the
   version it started on, so editing the working copy cannot rewrite the paper
   underneath someone who is mid-exam.                                       */

export type ExamVersion = {
  examId: string
  version: number
  publishedAt: number
  note: string
  exam: Exam
}

const VERSIONS_KEY = 'am.versions.v1'

export const listVersions = (examId: string) =>
  read<ExamVersion[]>(VERSIONS_KEY, [])
    .filter((v) => v.examId === examId)
    .sort((a, b) => b.version - a.version)

/** Freeze the working copy as `exam.version`, replacing any prior snapshot of it. */
export function snapshotVersion(exam: Exam, note = ''): ExamVersion {
  const all = read<ExamVersion[]>(VERSIONS_KEY, [])
  const snap: ExamVersion = {
    examId: exam.id,
    version: exam.version,
    publishedAt: Date.now(),
    note,
    exam: JSON.parse(JSON.stringify(exam)) as Exam,
  }
  write(VERSIONS_KEY, [...all.filter((v) => !(v.examId === exam.id && v.version === exam.version)), snap])
  return snap
}

/**
 * Records that claim a published version but predate snapshotting have nothing
 * frozen behind them, so a pinned attempt would silently fall back to the live
 * working copy — exactly the guarantee versions exist to make. Backfill once.
 */
export function backfillVersions(exams: Exam[]) {
  const all = read<ExamVersion[]>(VERSIONS_KEY, [])
  const have = new Set(all.map((v) => `${v.examId}@${v.version}`))
  const missing = exams.filter(
    (e) => e.publishedVersion !== undefined && !have.has(`${e.id}@${e.publishedVersion}`),
  )
  if (missing.length === 0) return
  write(VERSIONS_KEY, [
    ...all,
    ...missing.map<ExamVersion>((e) => ({
      examId: e.id,
      version: e.publishedVersion!,
      publishedAt: e.updatedAt,
      note: 'backfilled',
      exam: JSON.parse(JSON.stringify(e)) as Exam,
    })),
  ])
}

export function dropVersions(examId: string) {
  write(VERSIONS_KEY, read<ExamVersion[]>(VERSIONS_KEY, []).filter((v) => v.examId !== examId))
}

/** What a given attempt should actually render: its pinned version, if we have it. */
export function resolveExam(current: Exam, version?: number): Exam {
  if (version === undefined || version === current.version) return current
  return listVersions(current.id).find((v) => v.version === version)?.exam ?? current
}

const PEOPLE_KEY = 'am.people.v1'
const ASSIGN_KEY = 'am.assignments.v1'

const seedPeople = (): Person[] =>
  [
    ['Budi Santoso', 'budi.santoso@example.id', 'Backend'],
    ['Sari Dewi', 'sari.dewi@example.id', 'Backend'],
    ['Andi Pratama', 'andi.pratama@example.id', 'Data'],
    ['Maya Kusuma', 'maya.kusuma@example.id', 'Produk'],
    ['Rizky Hidayat', 'rizky.hidayat@example.id', 'Data'],
  ].map(([name, email, group], i) => ({
    id: `p_seed_${i}`,
    name: name!,
    email: email!,
    group: group!,
    createdAt: Date.now() - i * 1000,
  }))

export function listPeople(): Person[] {
  const xs = read<Person[]>(PEOPLE_KEY, [])
  if (xs.length === 0) {
    const fresh = seedPeople()
    write(PEOPLE_KEY, fresh)
    return fresh
  }
  return xs
}

export function savePerson(p: Person): Person[] {
  const xs = listPeople()
  const i = xs.findIndex((x) => x.id === p.id)
  if (i >= 0) xs[i] = p
  else xs.unshift(p)
  write(PEOPLE_KEY, xs)
  return xs
}

export function deletePerson(id: string): Person[] {
  const xs = listPeople().filter((p) => p.id !== id)
  write(PEOPLE_KEY, xs)
  // An assignment without a person is a dangling invitation; drop those too.
  write(ASSIGN_KEY, listAssignments().filter((a) => a.personId !== id))
  return xs
}

/**
 * Accepts the shapes people actually paste: a bare email, "Name <email>",
 * or comma/tab separated "Name, email, group". Existing emails are skipped
 * rather than duplicated — re-pasting a roster must not double it.
 */
export function importPeople(text: string): { added: Person[]; skipped: number } {
  const existing = listPeople()
  const seen = new Set(existing.map((p) => p.email.toLowerCase()))
  const added: Person[] = []
  let skipped = 0

  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (!line) continue

    let name = ''
    let email = ''
    let group = ''

    const angled = line.match(/^(.*?)<([^>]+)>\s*$/)
    if (angled) {
      name = angled[1]!.trim()
      email = angled[2]!.trim()
    } else {
      const parts = line.split(/[,\t;]/).map((x) => x.trim())
      const at = parts.find((x) => x.includes('@'))
      if (!at) continue
      email = at
      name = parts.filter((x) => x !== at)[0] ?? ''
      group = parts.filter((x) => x !== at)[1] ?? ''
    }
    if (!email.includes('@')) continue
    if (seen.has(email.toLowerCase())) {
      skipped++
      continue
    }
    seen.add(email.toLowerCase())
    added.push({
      id: uid('p'),
      name: name || email.split('@')[0]!,
      email,
      group,
      createdAt: Date.now(),
    })
  }

  if (added.length) write(PEOPLE_KEY, [...added, ...existing])
  return { added, skipped }
}

/**
 * Assign to addresses that are not on the roster. Anyone new is added to it —
 * an assignment needs a person to hang status and score on, so "off-roster"
 * means "you did not have to add them first", not "no record exists".
 */
export function ensurePeople(text: string): { ids: string[]; created: number } {
  const before = listPeople()
  const byEmail = new Map(before.map((p) => [p.email.toLowerCase(), p]))
  const { added } = importPeople(text)
  added.forEach((p) => byEmail.set(p.email.toLowerCase(), p))

  const ids: string[] = []
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (!line) continue
    const m = line.match(/<([^>]+)>/) ?? line.match(/([^\s,;\t]+@[^\s,;\t]+)/)
    const email = m?.[1]?.trim().toLowerCase()
    const hit = email ? byEmail.get(email) : undefined
    if (hit && !ids.includes(hit.id)) ids.push(hit.id)
  }
  return { ids, created: added.length }
}

/** A person as a file or a form supplies one, before it has an id. */
export type PersonDraft = { name: string; email: string; group: string }

/**
 * The same promise as `ensurePeople`, for rows that have already been parsed
 * and validated (roster.ts): everyone in the list ends up on the roster once,
 * and the ids come back in file order whether they were new or not.
 */
export function ensureDrafts(drafts: PersonDraft[]): { ids: string[]; created: number } {
  const existing = listPeople()
  const byEmail = new Map(existing.map((p) => [p.email.toLowerCase(), p]))
  const added: Person[] = []
  const ids: string[] = []
  for (const d of drafts) {
    const key = d.email.toLowerCase()
    let p = byEmail.get(key)
    if (!p) {
      p = { id: uid('p'), name: d.name || d.email.split('@')[0]!, email: d.email, group: d.group, createdAt: Date.now() }
      byEmail.set(key, p)
      added.push(p)
    }
    if (!ids.includes(p.id)) ids.push(p.id)
  }
  if (added.length) write(PEOPLE_KEY, [...added, ...existing])
  return { ids, created: added.length }
}

/**
 * Records written before links were hashed carried a plaintext `token`. It is
 * dropped on read, not converted: a link that was ever stored in the clear is
 * not one to keep honouring, so those invitations read as "not sent".
 */
const normaliseAssignment = (a: Assignment): Assignment => {
  const { token: _legacy, ...rest } = a as Assignment & { token?: string }
  return {
    ...rest,
    tokenHash: rest.tokenHash ?? '',
    retiredHashes: rest.retiredHashes ?? [],
    otpSends: rest.otpSends ?? 0,
    selfServes: rest.selfServes ?? 0,
    history: rest.history ?? [],
  }
}

export function listAssignments(): Assignment[] {
  return read<Assignment[]>(ASSIGN_KEY, []).map(normaliseAssignment)
}

export const writeAssignments = (xs: Assignment[]) => write(ASSIGN_KEY, xs)

export const assignmentsFor = (examId: string) => listAssignments().filter((a) => a.examId === examId)

/**
 * One assignment per person per instrument; re-assigning someone is a no-op.
 * This only records who is to sit what. The link is minted when the invitation
 * is sent (invite.ts), so an assignment to a draft holds no token at all.
 */
export function assignPeople(
  examId: string,
  personIds: string[],
  dueAt?: number,
  examVersion = 1,
): Assignment[] {
  const all = listAssignments()
  const have = new Set(all.filter((a) => a.examId === examId).map((a) => a.personId))
  const fresh = personIds
    .filter((pid) => !have.has(pid))
    .map<Assignment>((personId) => ({
      id: uid('as'),
      examId,
      examVersion,
      personId,
      assignedAt: Date.now(),
      dueAt,
      status: 'invited',
      tokenHash: '',
      retiredHashes: [],
      otpSends: 0,
      selfServes: 0,
      history: [],
    }))
  if (fresh.length) writeAssignments([...all, ...fresh])
  return fresh
}

export function updateAssignment(id: string, patch: Partial<Assignment>) {
  writeAssignments(listAssignments().map((a) => (a.id === id ? { ...a, ...patch } : a)))
}

export function unassign(id: string) {
  writeAssignments(listAssignments().filter((a) => a.id !== id))
}

const responsesKey = (examId: string) => `am.responses.v1.${examId}`

export function listResponses(examId: string): Attempt[] {
  return read<Attempt[]>(responsesKey(examId), [])
}

export function addResponse(a: Attempt) {
  write(responsesKey(a.examId), [...listResponses(a.examId), a])
}

export function clearResponses(examId: string) {
  try {
    localStorage.removeItem(responsesKey(examId))
  } catch { /* ignore */ }
}

export type Aggregate = {
  item: Item
  n: number
  /** choice id (or scale value as a string) → how many people picked it */
  counts: Record<string, number>
  mean?: number
  texts: string[]
}

/** What "results" means when nothing is graded: a distribution, not a score. */
export function aggregate(exam: Exam, rs: Attempt[]): Aggregate[] {
  return allItems(exam).map((item) => {
    const counts: Record<string, number> = {}
    const texts: string[] = []
    let n = 0
    let sum = 0
    let numeric = 0

    for (const r of rs) {
      const picked = r.answers[item.id] ?? []
      const typed = (r.text[item.id] ?? '').trim()
      if (picked.length === 0 && !typed) continue
      n++
      for (const v of picked) {
        counts[v] = (counts[v] ?? 0) + 1
        if (isScale(item.type)) {
          const num = Number(v)
          if (!Number.isNaN(num)) {
            sum += num
            numeric++
          }
        }
      }
      if (typed) texts.push(typed)
    }

    return { item, n, counts, texts, mean: numeric > 0 ? sum / numeric : undefined }
  })
}

/** Required questions the participant has not answered yet. */
export function unanswered(exam: Exam, a: Attempt): Item[] {
  return allItems(exam).filter(
    (i) => i.required && (a.answers[i.id]?.length ?? 0) === 0 && !(a.text[i.id] ?? '').trim(),
  )
}

export function newAttempt(exam: Exam): Attempt {
  return {
    examId: exam.id,
    answers: {},
    text: {},
    files: {},
    flagged: [],
    startedAt: Date.now(),
    secondsLeft: totalMinutes(exam) * 60,
    events: [{ seq: 1, t: Date.now(), kind: 'attempt.started', detail: `Tier ${exam.tier}` }],
    queued: 0,
  }
}

export const append = (a: Attempt, kind: string, detail?: string): AttemptEvent[] => [
  ...a.events,
  { seq: a.events.length + 1, t: Date.now(), kind, detail },
]

/** Auto-scoring. Essays are never auto-scored — they are excluded from the machine total. */
export function score(exam: Exam, a: Attempt) {
  let raw = 0
  let max = 0
  let pendingHuman = 0

  for (const it of allItems(exam)) {
    if (it.type === 'essay' || isScale(it.type)) {
      pendingHuman += it.points
      continue
    }
    max += it.points
    if (it.type === 'short') {
      const given = (a.text[it.id] ?? '').trim().toLowerCase()
      if (given && it.correct.some((k) => k.trim().toLowerCase() === given)) raw += it.points
      continue
    }
    const given = [...(a.answers[it.id] ?? [])].sort()
    const want = [...it.correct].sort()
    if (given.length > 0 && given.length === want.length && given.every((g, i) => g === want[i]))
      raw += it.points
  }

  const pct = max === 0 ? 0 : Math.round((raw / max) * 100)
  return { raw, max, pct, passed: pct >= exam.passPct, pendingHuman }
}
