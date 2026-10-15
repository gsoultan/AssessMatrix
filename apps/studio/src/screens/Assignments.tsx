import { Fragment, useEffect, useMemo, useState } from 'react'
import { TextInput, Textarea, Badge, Tooltip, Collapse, Drawer, Select } from '@mantine/core'
import {
  IconSend,
  IconCheck,
  IconTrash,
  IconSearch,
  IconAlertTriangle,
  IconUsers,
  IconMailPlus,
  IconMailForward,
  IconBan,
  IconHistory,
  IconInbox,
  IconListDetails,
  IconArrowLeft,
  IconChevronRight,
  IconUserPlus,
} from '@tabler/icons-react'
import { useApp, useResponses } from '../state'
import { AnswerSheet } from '../components/AnswerSheet'
import { CORE } from '../backend/mode'
import { Panel } from '../components/Panel'
import { RosterImport } from '../components/RosterImport'
import { type LinkState, EVENT_LABEL, linkState, listOutbox, sittingClosed } from '../invite'
import {
  type AssignStatus,
  type Assignment,
  type Exam,
  type ExamVersion,
  KINDS,
  allItems,
  getAttempt,
  isScored,
  resolveExam,
} from '../store'

/**
 * What the table shows. The stored status has no step between "invited" and
 * "started"; the invitation's own history does: the link was opened, a code
 * was asked for or verified, and nothing was started yet.
 */
type Seen = AssignStatus | 'opened'

const OPENED_KINDS = new Set(['link.opened', 'code.sent', 'code.wrong', 'link.redeemed'])

function seen(a: Assignment): Seen {
  if (a.status !== 'invited') return a.status
  if (linkState(a) === 'expired') return 'expired'
  const since = a.sentAt ?? 0
  return a.redeemedAt || a.history.some((h) => OPENED_KINDS.has(h.kind) && h.t >= since) ? 'opened' : 'invited'
}

const SEEN_LABEL: Record<Seen, string> = {
  invited: 'Invited',
  opened: 'Opened',
  started: 'In progress',
  submitted: 'Completed',
  expired: 'Expired',
}

const SEEN_HINT: Record<Seen, string> = {
  invited: 'Invitation sent; the link has not been opened yet',
  opened: 'Opened the link, but has not started the test',
  started: 'Started the test and has not handed it in yet',
  submitted: 'Handed in',
  expired: 'The link ran out before it was used. You can invite them again',
}

const STATUS_STYLE: Record<Seen, { bg: string; fg: string }> = {
  invited: { bg: 'var(--color-sunken)', fg: 'var(--color-ink-2)' },
  opened: { bg: 'var(--color-wash-warn)', fg: 'var(--color-serious)' },
  started: { bg: 'var(--color-wash)', fg: 'var(--color-brand-text)' },
  submitted: { bg: 'var(--color-wash-good)', fg: 'var(--color-good-text)' },
  expired: { bg: 'var(--color-wash-warn)', fg: 'var(--color-serious)' },
}

const STATUS_DOT: Record<Seen, string> = {
  invited: 'var(--color-ink-muted)',
  opened: 'var(--color-serious)',
  started: 'var(--color-s1)',
  submitted: 'var(--color-good-fill)',
  expired: 'var(--color-serious)',
}

function StatusPill({ status }: { status: Seen }) {
  const s = STATUS_STYLE[status]
  return (
    <span
      title={SEEN_HINT[status]}
      className="inline-flex items-center rounded px-1.5 py-0.5 text-[10.5px] font-semibold uppercase tracking-wide"
      style={{ background: s.bg, color: s.fg }}
    >
      {SEEN_LABEL[status]}
    </span>
  )
}

const when = (ts?: number) => (ts ? new Date(ts).toLocaleString(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : '—')

/** Where the emailed link stands. The link itself is not shown: only its hash is kept. */
function LinkLine({ a }: { a: Assignment }) {
  const state: LinkState = linkState(a)
  const text: Record<LinkState, string> = {
    unsent: 'Not sent yet',
    live: `Sent ${when(a.sentAt)} · expires ${when(a.expiresAt)}`,
    used: `Opened ${when(a.redeemedAt)}`,
    expired: `Link expired ${when(a.expiresAt)}`,
    locked: 'Locked — too many codes requested',
    revoked: `Revoked ${when(a.revokedAt)}`,
  }
  const bad = state === 'expired' || state === 'locked' || state === 'revoked'
  return (
    <p
      data-testid={`link-${state}`}
      className="tnum mt-1 text-[11px] leading-snug"
      style={{ color: bad ? 'var(--color-critical)' : 'var(--color-ink-muted)' }}
    >
      {text[state]}
    </p>
  )
}

function History({ a }: { a: Assignment }) {
  if (a.history.length === 0) return <p className="text-[12px] text-ink-muted">Nothing has happened to this invitation yet.</p>
  return (
    <ol className="flex flex-col gap-1">
      {a.history.map((e) => (
        <li key={e.seq} className="tnum flex gap-3 text-[12px] leading-snug">
          <span className="w-[104px] shrink-0 text-ink-muted">{when(e.t)}</span>
          <span className="text-ink-2">
            {EVENT_LABEL[e.kind] ?? e.kind}
            {e.detail && <span className="text-ink-muted"> · {e.detail}</span>}
          </span>
        </li>
      ))}
    </ol>
  )
}

/** "Invited 3 people, 1 added to the roster. 3 emails sent." */
const invitedNote = (r: { invited: number; created: number; sent: number }, live: boolean) => {
  if (r.invited === 0) return 'No one new to invite.'
  const who = `Invited ${r.invited}${r.created ? `, ${r.created} added to the roster` : ''}.`
  return live ? `${who} ${r.sent} ${r.sent === 1 ? 'email' : 'emails'} sent.` : `${who} Emails are held until this is published.`
}

/**
 * One candidate's submitted answers. Locally the attempt is in this browser;
 * against the core it is that assignment's response. An anonymous
 * instrument has no such view: its answers are not tied to anyone.
 */
function CandidateAnswers({ exam, a }: { exam: Exam; a: Assignment }) {
  const responses = useResponses(exam.id)
  const load = useApp((s) => s.loadResponses)
  // The list may have been read before this person handed in; read it again on opening.
  const [fresh, setFresh] = useState(!CORE)
  useEffect(() => {
    if (CORE) void load(exam.id).finally(() => setFresh(true))
  }, [exam.id, load])
  const attempt = CORE ? responses.find((r) => r.assignmentId === a.id) : getAttempt(exam.id, a.id)
  if (!attempt?.submittedAt)
    return <p className="py-6 text-center text-[13px] text-ink-muted">{fresh ? 'No submitted answers found.' : 'Loading…'}</p>
  const sat = resolveExam(exam, attempt.examVersion ?? a.examVersion)
  return (
    <>
      <p className="tnum mb-3 text-[12.5px] text-ink-2">
        Submitted {new Date(attempt.submittedAt).toLocaleString()} · v{sat.version}
        {attempt.score && ` · ${attempt.score.raw} / ${attempt.score.max} points (${attempt.score.pct}%)`}
      </p>
      <AnswerSheet exam={sat} attempt={attempt} />
    </>
  )
}

type Stage = 'all' | 'open' | 'done' | 'none'

const STAGES: { v: Stage; label: string; hint: string }[] = [
  { v: 'all', label: 'All', hint: '' },
  { v: 'open', label: 'Open', hint: 'Someone invited has not handed in yet' },
  { v: 'done', label: 'All completed', hint: 'Every invited person has handed in' },
  { v: 'none', label: 'No one invited', hint: 'Nobody has been invited yet' },
]

/** Assignments starts here: every instrument, with how far its invitations have got. Pick one to assign people. */
function InstrumentList({ exams, assignments, onOpen }: { exams: Exam[]; assignments: Assignment[]; onOpen: (id: string) => void }) {
  const [stage, setStage] = useState<Stage>('all')
  const [find, setFind] = useState('')
  const rows = exams.map((e) => {
    const mine = assignments.filter((a) => a.examId === e.id)
    const done = mine.filter((a) => a.status === 'submitted').length
    const going = mine.filter((a) => a.status === 'started').length
    const opened = mine.filter((a) => seen(a) === 'opened').length
    const stageOf: Stage = mine.length === 0 ? 'none' : done === mine.length ? 'done' : 'open'
    return { e, total: mine.length, done, going, opened, stage: stageOf }
  })
  const t = find.trim().toLowerCase()
  const shown = rows.filter((r) => (stage === 'all' || r.stage === stage) && (!t || r.e.title.toLowerCase().includes(t)))

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-4">
        <h1 className="text-[15px] font-semibold tracking-tight">Assignments</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Pick an instrument to invite people to it and follow their progress. Each person gets their own link by
          email.
        </p>
      </header>
      {exams.length === 0 ? (
        <p className="hair rounded-xl bg-surface py-12 text-center text-[13px] text-ink-muted">
          Nothing to assign yet — create an instrument in the Library first.
        </p>
      ) : (
        <>
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <div role="group" aria-label="Filter instruments" className="flex flex-wrap gap-1">
              {STAGES.map((st) => {
                const on = stage === st.v
                const n = st.v === 'all' ? rows.length : rows.filter((r) => r.stage === st.v).length
                return (
                  <button
                    key={st.v}
                    onClick={() => setStage(st.v)}
                    aria-pressed={on}
                    title={st.hint || undefined}
                    className="hair inline-flex items-center gap-1.5 rounded-md bg-surface px-2.5 py-1 text-[12px] font-medium"
                    style={on ? { background: 'var(--color-wash)', color: 'var(--color-brand-text)' } : { color: 'var(--color-ink-2)' }}
                  >
                    {st.label} <span className="tnum text-ink-muted">{n}</span>
                  </button>
                )
              })}
            </div>
            <TextInput
              size="xs"
              className="ml-auto w-full sm:w-[220px]"
              placeholder="Find an instrument"
              aria-label="Find an instrument"
              leftSection={<IconSearch size={13} stroke={1.8} />}
              value={find}
              onChange={(e) => setFind(e.currentTarget.value)}
            />
          </div>
          <div className="hair divide-y divide-hair rounded-xl bg-surface">
            {shown.length === 0 && <p className="py-10 text-center text-[12.5px] text-ink-muted">No instrument matches.</p>}
            {shown.map(({ e, total, done, going, opened }) => (
              <button
                key={e.id}
                onClick={() => onOpen(e.id)}
                aria-label={`Open ${e.title}`}
                className="flex w-full items-center gap-4 px-4 py-3.5 text-left hover:bg-ink/[0.03] sm:px-5"
              >
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13.5px] font-medium text-ink">{e.title}</p>
                  <p className="tnum truncate text-[12px] text-ink-muted">
                    {KINDS.find((k) => k.v === e.kind)?.label} · {e.status === 'published' ? 'Published' : 'Draft'} ·{' '}
                    {allItems(e).length} questions
                  </p>
                </div>
                <div className="hidden w-[260px] shrink-0 sm:block">
                  {total === 0 ? (
                    <p className="text-right text-[12px] text-ink-muted">No one invited yet</p>
                  ) : (
                    <>
                      <p className="tnum text-right text-[12px] text-ink-2">
                        <strong className="font-semibold text-ink">{done}</strong> of {total} completed
                        {going > 0 && <span className="text-ink-muted"> · {going} in progress</span>}
                        {opened > 0 && <span className="text-ink-muted"> · {opened} opened</span>}
                      </p>
                      <div className="mt-1.5 flex h-1.5 w-full overflow-hidden rounded-full" style={{ background: 'var(--color-track)' }}>
                        <div style={{ width: `${(done / total) * 100}%`, background: 'var(--color-good-fill)' }} />
                        <div style={{ width: `${(going / total) * 100}%`, background: 'var(--color-s1)' }} />
                        <div style={{ width: `${(opened / total) * 100}%`, background: 'var(--color-serious)', opacity: 0.55 }} />
                      </div>
                    </>
                  )}
                </div>
                <IconChevronRight size={16} stroke={2} className="shrink-0 text-ink-muted" />
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

export function Assignments({ isDesktop }: { isDesktop: boolean }) {
  const exams = useApp((s) => s.exams)
  const people = useApp((s) => s.people)
  const assignments = useApp((s) => s.assignments)
  const assign = useApp((s) => s.assign)
  const assignByEmail = useApp((s) => s.assignByEmail)
  const assignDrafts = useApp((s) => s.assignDrafts)
  const unassignOne = useApp((s) => s.unassignOne)
  const sendInvite = useApp((s) => s.sendInvite)
  const revokeInviteFor = useApp((s) => s.revokeInviteFor)
  const assignTarget = useApp((s) => s.assignTarget)
  const setScreen = useApp((s) => s.setScreen)

  const active = useMemo(() => exams.filter((e) => e.status !== 'archived'), [exams])
  // The list of instruments comes first; one is open only when picked, or when
  // the Library's "Assign people" asked for it.
  const [examId, setExamId] = useState<string>(() => assignTarget ?? '')
  const exam: Exam | undefined = active.find((e) => e.id === examId)

  const [picked, setPicked] = useState<string[]>([])
  const [group, setGroup] = useState('all')
  const [q, setQ] = useState('')
  const [due, setDue] = useState('')
  const [note, setNote] = useState('')
  const [historyOf, setHistoryOf] = useState('')
  const [answersOf, setAnswersOf] = useState('')
  const [show, setShow] = useState<Seen | 'all'>('all')
  const [find, setFind] = useState('')
  const [adhocOpen, setAdhocOpen] = useState(false)
  const [adhoc, setAdhoc] = useState('')
  const [tab, setTab] = useState<'people' | 'outbox'>('people')
  // Arriving from the Library's "Assign people" goes straight to inviting.
  const [inviting, setInviting] = useState(() => !!assignTarget)

  const rows = useMemo(
    () => (exam ? assignments.filter((a) => a.examId === exam.id) : []),
    [assignments, exam],
  )
  // Each person's latest sitting: they can be invited again only once it is closed.
  const lastOf = useMemo(() => {
    const m = new Map<string, Assignment>()
    for (const r of rows) if ((m.get(r.personId)?.sitting ?? 0) < r.sitting) m.set(r.personId, r)
    return m
  }, [rows])
  const personOf = (id: string) => people.find((p) => p.id === id)

  const groups = useMemo(() => [...new Set(people.map((p) => p.group).filter(Boolean))].sort(), [people])
  const candidates = useMemo(
    () =>
      people
        .filter((p) => !lastOf.has(p.id) || sittingClosed(lastOf.get(p.id)!))
        .filter((p) => group === 'all' || p.group === group)
        .filter((p) => {
          const t = q.trim().toLowerCase()
          return !t || p.name.toLowerCase().includes(t) || p.email.toLowerCase().includes(t)
        }),
    [people, lastOf, group, q],
  )

  const live = exam?.status === 'published'
  const dueAt = due ? new Date(due).getTime() : undefined
  // Frozen versions a new sitting can be given; '' is the live one.
  const [versions, setVersions] = useState<ExamVersion[]>([])
  const [pickVersion, setPickVersion] = useState('')
  const examVersion = pickVersion ? Number(pickVersion) : undefined
  const listVersions = useApp((s) => s.listVersions)
  const shownId = exam?.id
  const published = exam?.publishedVersion
  useEffect(() => {
    setPickVersion('')
    if (!inviting || !shownId || !published) return setVersions([])
    let live = true
    listVersions(shownId).then((vs) => live && setVersions(vs.filter((v) => v.version <= published)))
    return () => {
      live = false
    }
  }, [inviting, shownId, published, listVersions])
  const unsent = rows.filter((r) => linkState(r) === 'unsent' && r.status !== 'submitted')
  // Every send writes an assignment, so this re-reads exactly when there is something new.
  const mails = useMemo(() => {
    const mine = new Set(rows.map((r) => r.id))
    // Against the core, mail goes to real inboxes; there is no outbox to show.
    return CORE ? [] : listOutbox().filter((m) => mine.has(m.assignmentId)).slice(0, 12)
  }, [rows])

  const resend = async (a: Assignment, name: string) => {
    const n = await sendInvite([a.id])
    setNote(n ? `New link emailed to ${name}. The previous one no longer works.` : 'Nothing sent — publish the instrument first.')
  }

  const count = (s: Seen) => rows.filter((r) => seen(r) === s).length
  // A person's sittings sit together, in order, where their first one was.
  const firstAt = new Map<string, number>()
  rows.forEach((r, i) => firstAt.has(r.personId) || firstAt.set(r.personId, i))
  const multi = (r: Assignment) => (lastOf.get(r.personId)?.sitting ?? 1) > 1
  const shown = [...rows].sort((a, b) => firstAt.get(a.personId)! - firstAt.get(b.personId)! || a.sitting - b.sitting).filter(
    (r) =>
      (show === 'all' || seen(r) === show) &&
      (() => {
        const t = find.trim().toLowerCase()
        const p = personOf(r.personId)
        return !t || !!p?.name.toLowerCase().includes(t) || !!p?.email.toLowerCase().includes(t)
      })(),
  )

  const open = (id: string) => {
    setExamId(id)
    setPicked([])
    setNote('')
    setHistoryOf('')
    setAnswersOf('')
    setShow('all')
    setFind('')
    setTab('people')
    setInviting(false)
  }

  if (!exam) return <InstrumentList exams={active} assignments={assignments} onOpen={open} />

  const held = live && unsent.length > 0 && (
    <button
      onClick={async () => {
        const n = await sendInvite(unsent.map((r) => r.id))
        setNote(`${n} held ${n === 1 ? 'invitation' : 'invitations'} emailed.`)
      }}
      className="hair inline-flex h-[30px] items-center gap-1.5 rounded-lg bg-surface px-3 text-[12.5px] font-medium text-ink-2"
    >
      <IconSend size={13} stroke={1.9} /> Send {unsent.length} held
    </button>
  )
  // Close the drawer once something went out, so the new rows are in view.
  const invited = (msg: string, done: boolean) => {
    setNote(msg)
    if (done) {
      setInviting(false)
      setTab('people')
    }
  }

  return (
    <div className="p-4 sm:p-6">
      <button
        onClick={() => open('')}
        className="mb-3 inline-flex items-center gap-1 text-[12.5px] font-medium text-ink-2"
      >
        <IconArrowLeft size={14} stroke={2} /> All instruments
      </button>
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-wider text-ink-muted">
            {KINDS.find((k) => k.v === exam.kind)?.label} · v{exam.publishedVersion ?? exam.version} · {allItems(exam).length} questions
          </p>
          <h1 className="text-[17px] font-semibold tracking-tight">{exam.title}</h1>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={() => setScreen('people')}
            className="hair inline-flex h-[30px] items-center gap-1.5 rounded-lg bg-surface px-3 text-[12.5px] font-medium text-ink-2"
          >
            <IconUsers size={14} stroke={1.9} /> Manage roster
          </button>
          <button
            onClick={() => { setNote(''); setInviting(true) }}
            className="inline-flex h-[30px] items-center gap-1.5 rounded-lg px-3 text-[12.5px] font-semibold text-on-brand"
            style={{ background: 'var(--color-s1)' }}
          >
            <IconUserPlus size={14} stroke={2} /> Invite people
          </button>
        </div>
      </header>

      {exam.status !== 'published' && (
        <p
          className="mb-4 flex items-start gap-2 rounded-lg p-3 text-[12.5px] leading-snug"
          style={{ background: 'var(--color-wash-warn)', color: 'var(--color-ink-2)' }}
        >
          <IconAlertTriangle size={14} stroke={2.1} className="mt-px shrink-0" style={{ color: 'var(--color-serious)' }} />
          <span>
            This is a <strong className="font-medium text-ink">draft</strong>. You can assign people
            now, but no email goes out for something that cannot be opened. Publish it, then send
            the held invitations from here.
          </span>
        </p>
      )}

      {rows.length > 0 && (
        <div className="mb-4">
          <div className="flex items-baseline justify-between gap-3">
            <p className="tnum text-[12.5px] text-ink-2">
              <strong className="font-semibold text-ink">{count('submitted')}</strong> of {rows.length} completed
            </p>
            <p className="tnum text-[12px] text-ink-muted">{Math.round((count('submitted') / rows.length) * 100)}%</p>
          </div>
          <div className="mt-1.5 flex h-2 w-full overflow-hidden rounded-full" style={{ background: 'var(--color-track)' }}>
            <div style={{ width: `${(count('submitted') / rows.length) * 100}%`, background: 'var(--color-good-fill)' }} />
            <div style={{ width: `${(count('started') / rows.length) * 100}%`, background: 'var(--color-s1)' }} />
            <div style={{ width: `${(count('opened') / rows.length) * 100}%`, background: 'var(--color-serious)', opacity: 0.55 }} />
          </div>
        </div>
      )}

      {note && !inviting && (
        <p role="status" className="mb-3 text-[12.5px] text-ink-2">
          {note}
        </p>
      )}

      <div className="mb-3 flex flex-wrap items-end justify-between gap-2 border-b border-hair">
        <div role="tablist" aria-label="Instrument sections" className="flex gap-4">
          {(CORE ? (['people'] as const) : (['people', 'outbox'] as const)).map((t) => {
            const on = tab === t
            return (
              <button
                key={t}
                role="tab"
                aria-selected={on}
                onClick={() => setTab(t)}
                className="-mb-px inline-flex items-center gap-1.5 border-b-2 px-0.5 pb-2 text-[13px] font-medium"
                style={on ? { borderColor: 'var(--color-s1)', color: 'var(--color-ink)' } : { borderColor: 'transparent', color: 'var(--color-ink-muted)' }}
              >
                {t === 'people' ? <IconUsers size={14} stroke={1.9} /> : <IconInbox size={14} stroke={1.9} />}
                {t === 'people' ? 'People' : 'Outbox'}
                <span className="tnum rounded-full px-1.5 text-[11px]" style={{ background: 'var(--color-sunken)' }}>
                  {t === 'people' ? rows.length : mails.length}
                </span>
              </button>
            )
          })}
        </div>
        {tab === 'people' && held && <div className="pb-2">{held}</div>}
      </div>

      {tab === 'people' && (
        <Panel pad={rows.length === 0}>
          {rows.length > 0 && (
            <div className="flex flex-wrap items-center gap-2 border-b border-hair px-4 py-2.5 sm:px-5">
              <div role="group" aria-label="Filter by status" className="flex flex-wrap gap-1">
                {(['all', 'invited', 'opened', 'started', 'submitted', 'expired'] as const)
                  .filter((v) => v === 'all' || v !== 'expired' || count('expired') > 0)
                  .map((v) => {
                    const on = show === v
                    const n = v === 'all' ? rows.length : count(v)
                    return (
                      <button
                        key={v}
                        onClick={() => setShow(v)}
                        aria-pressed={on}
                        title={v === 'all' ? undefined : SEEN_HINT[v]}
                        className="hair inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px] font-medium"
                        style={on ? { background: 'var(--color-wash)', color: 'var(--color-brand-text)' } : { color: 'var(--color-ink-2)' }}
                      >
                        {v !== 'all' && <span className="h-2 w-2 rounded-full" style={{ background: STATUS_DOT[v] }} />}
                        {v === 'all' ? 'All' : SEEN_LABEL[v]}
                        <span className="tnum text-ink-muted">{n}</span>
                      </button>
                    )
                  })}
              </div>
              <TextInput
                size="xs"
                className="ml-auto w-full sm:w-[200px]"
                placeholder="Find a person"
                aria-label="Find a person"
                leftSection={<IconSearch size={13} stroke={1.8} />}
                value={find}
                onChange={(e) => setFind(e.currentTarget.value)}
              />
            </div>
          )}
          {rows.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-10 text-center">
              <p className="text-[12.5px] text-ink-muted">No one invited yet. Each person you invite is emailed their own link.</p>
              <button
                onClick={() => { setNote(''); setInviting(true) }}
                className="inline-flex h-[30px] items-center gap-1.5 rounded-lg px-3 text-[12.5px] font-semibold text-on-brand"
                style={{ background: 'var(--color-s1)' }}
              >
                <IconUserPlus size={14} stroke={2} /> Invite people
              </button>
            </div>
          ) : shown.length === 0 ? (
            <p className="py-10 text-center text-[12.5px] text-ink-muted">No one here matches.</p>
          ) : isDesktop ? (
            <div className="scroll-thin overflow-x-auto">
              <table className="w-full min-w-[620px] text-left">
                <thead>
                  <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                    <th className="px-4 py-2 font-medium sm:px-5">Person</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                    <th className="px-3 py-2 font-medium">Version</th>
                    <th className="px-3 py-2 font-medium">Started</th>
                    <th className="px-3 py-2 font-medium">Submitted</th>
                    {isScored(exam) && <th className="px-3 py-2 text-right font-medium">Score</th>}
                    <th className="px-4 py-2 text-right font-medium sm:px-5">Invitation</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((r) => {
                    const p = personOf(r.personId)
                    const name = `${p?.name ?? 'Unknown'}${r.sitting > 1 ? ` (sitting ${r.sitting})` : ''}`
                    const state = linkState(r)
                    const cols = isScored(exam) ? 7 : 6
                    return (
                      <Fragment key={r.id}>
                      <tr className="border-b border-hair/60 last:border-0">
                        <td className="px-4 py-3 sm:px-5">
                          <p className="text-[13px] font-medium text-ink">{p?.name ?? 'Unknown'}</p>
                          <p className="tnum text-[11px] text-ink-muted">{p?.email}</p>
                          {r.status === 'submitted' && !exam.anonymous && (
                            <button
                              onClick={() => setAnswersOf(r.id)}
                              aria-label={`View answers from ${name}`}
                              className="mt-1 inline-flex items-center gap-1 text-[11.5px] font-medium"
                              style={{ color: 'var(--color-brand-text)' }}
                            >
                              <IconListDetails size={13} stroke={1.9} /> View answers
                            </button>
                          )}
                        </td>
                        <td className="px-3 py-3">
                          <StatusPill status={seen(r)} />
                          <LinkLine a={r} />
                        </td>
                        <td className="tnum px-3 py-3 text-[12px] text-ink-muted">
                          v{r.examVersion}
                          {multi(r) && <span className="block text-[11px]">Sitting {r.sitting}</span>}
                        </td>
                        <td className="tnum px-3 py-3 text-[12px] text-ink-2">{when(r.startedAt)}</td>
                        <td className="tnum px-3 py-3 text-[12px] text-ink-2">{when(r.submittedAt)}</td>
                        {isScored(exam) && (
                          <td className="tnum px-3 py-3 text-right text-[12.5px]">
                            {r.scorePct === undefined ? (
                              <span className="text-ink-muted">—</span>
                            ) : (
                              <span style={{ color: r.passed ? 'var(--color-good-text)' : 'var(--color-critical)' }}>
                                {r.scorePct}%
                              </span>
                            )}
                          </td>
                        )}
                        <td className="px-4 py-3 sm:px-5">
                          <div className="flex items-center justify-end gap-1.5">
                            {r.status !== 'submitted' && (
                              <Tooltip
                                multiline
                                w={240}
                                label={
                                  state === 'unsent' && !r.sentAt
                                    ? 'Email this person their link'
                                    : 'Email a new link. The previous link stops working, and so does any session it opened. Answers saved on their device are kept.'
                                }
                                withArrow
                              >
                                <button
                                  onClick={() => resend(r, name)}
                                  disabled={!live}
                                  aria-label={`${r.sentAt ? 'Resend' : 'Send'} invitation to ${name}`}
                                  className="hair grid h-7 w-7 place-items-center rounded-md bg-surface disabled:opacity-40"
                                >
                                  <IconMailForward size={13} stroke={1.9} className="text-ink-2" />
                                </button>
                              </Tooltip>
                            )}
                            <Tooltip label="What has happened to this invitation" withArrow>
                              <button
                                onClick={() => setHistoryOf(historyOf === r.id ? '' : r.id)}
                                aria-expanded={historyOf === r.id}
                                aria-label={`Invitation history for ${name}`}
                                className="hair grid h-7 w-7 place-items-center rounded-md bg-surface"
                              >
                                <IconHistory size={13} stroke={1.9} className="text-ink-2" />
                              </button>
                            </Tooltip>
                            {state !== 'revoked' && state !== 'unsent' && r.status !== 'submitted' && (
                              <Tooltip
                                multiline
                                w={240}
                                label="Revoke: the link and any open session stop working at once. The row and its history are kept."
                                withArrow
                              >
                                <button
                                  onClick={() => revokeInviteFor(r.id)}
                                  aria-label={`Revoke invitation for ${name}`}
                                  className="hair grid h-7 w-7 place-items-center rounded-md bg-surface"
                                >
                                  <IconBan size={13} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
                                </button>
                              </Tooltip>
                            )}
                            <Tooltip label="Remove the assignment and its history" withArrow>
                              <button onClick={() => unassignOne(r.id)} aria-label={`Unassign ${name}`} className="hair grid h-7 w-7 place-items-center rounded-md bg-surface">
                                <IconTrash size={13} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
                              </button>
                            </Tooltip>
                          </div>
                        </td>
                      </tr>
                      {historyOf === r.id && (
                        <tr className="border-b border-hair/60 last:border-0">
                          <td colSpan={cols} className="px-4 py-3 sm:px-5" style={{ background: 'var(--color-sunken)' }}>
                            <History a={r} />
                          </td>
                        </tr>
                      )}
                      </Fragment>
                    )
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="flex flex-col gap-2 p-4">
              {shown.map((r) => {
                const p = personOf(r.personId)
                return (
                  <div key={r.id} className="hair rounded-xl bg-surface p-3.5">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="truncate text-[13.5px] font-medium text-ink">{p?.name}</p>
                        <p className="tnum truncate text-[11.5px] text-ink-muted">
                          {p?.email} · v{r.examVersion}
                          {multi(r) ? ` · sitting ${r.sitting}` : ''}
                        </p>
                      </div>
                      <StatusPill status={seen(r)} />
                    </div>
                    <LinkLine a={r} />
                    <details className="mt-1.5">
                      <summary className="cursor-pointer text-[11.5px] font-medium text-ink-muted">History</summary>
                      <div className="mt-1.5">
                        <History a={r} />
                      </div>
                    </details>
                    {r.scorePct !== undefined && (
                      <p className="tnum mt-1.5 text-[12px]" style={{ color: r.passed ? 'var(--color-good-text)' : 'var(--color-critical)' }}>
                        {r.scorePct}% · {r.passed ? 'lulus' : 'belum lulus'}
                      </p>
                    )}
                    {r.status === 'submitted' && !exam.anonymous && (
                      <button
                        onClick={() => setAnswersOf(r.id)}
                        aria-label={`View answers from ${p?.name}`}
                        className="hair mt-2.5 flex w-full items-center justify-center gap-1.5 rounded-lg bg-surface py-2 text-[12px] font-medium text-ink-2"
                      >
                        <IconListDetails size={12} stroke={1.9} /> View answers
                      </button>
                    )}
                    <div className="mt-2.5 flex items-center gap-2">
                      <button
                        onClick={() => resend(r, p?.name ?? 'them')}
                        disabled={!live || r.status === 'submitted'}
                        className="hair flex flex-1 items-center justify-center gap-1.5 rounded-lg bg-surface py-2 text-[12px] font-medium text-ink-2 disabled:opacity-40"
                      >
                        <IconMailForward size={12} stroke={1.9} /> {r.sentAt ? 'Resend' : 'Send'}
                      </button>
                      <button
                        onClick={() => revokeInviteFor(r.id)}
                        disabled={linkState(r) === 'revoked' || linkState(r) === 'unsent' || r.status === 'submitted'}
                        className="hair flex flex-1 items-center justify-center gap-1.5 rounded-lg bg-surface py-2 text-[12px] font-medium text-ink-2 disabled:opacity-40"
                      >
                        <IconBan size={12} stroke={1.9} /> Revoke
                      </button>
                      <button onClick={() => unassignOne(r.id)} aria-label={`Unassign ${p?.name}`} className="hair grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-surface">
                        <IconTrash size={13} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </Panel>
      )}

      {tab === 'outbox' && !CORE && (
        <Panel
          hint="No mail is sent yet. This lists what would have gone out, and it is the only place a link or a code can be read: the assignment keeps a hash, not the link. With a mail provider this tab goes away."
          title="What would have been emailed"
          pad={mails.length === 0}
        >
        {mails.length === 0 ? (
          <p className="py-4 text-center text-[12.5px] text-ink-muted">Nothing sent for this instrument yet.</p>
        ) : (
          <ul data-testid="outbox">
            {mails.map((m) => (
              <li key={m.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair/60 px-4 py-2.5 last:border-0 sm:px-5">
                <span className="tnum w-[104px] shrink-0 text-[11.5px] text-ink-muted">{when(m.at)}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[12.5px] font-medium text-ink">{m.subject}</span>
                  <span className="tnum block truncate text-[11.5px] text-ink-muted">to {m.to}</span>
                </span>
                {m.link && (
                  <a
                    href={m.link}
                    aria-label={`Open the link emailed to ${m.to}`}
                    className="hair shrink-0 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium"
                    style={{ color: 'var(--color-s1)' }}
                  >
                    Open as recipient
                  </a>
                )}
              </li>
            ))}
          </ul>
        )}
        </Panel>
      )}

      <Drawer
        opened={!!answersOf}
        onClose={() => setAnswersOf('')}
        position="right"
        size="lg"
        title={
          <span className="text-[14px] font-semibold">
            Answers · {personOf(rows.find((r) => r.id === answersOf)?.personId ?? '')?.name ?? ''}
          </span>
        }
      >
        {exam && rows.find((r) => r.id === answersOf) && (
          <CandidateAnswers exam={exam} a={rows.find((r) => r.id === answersOf)!} />
        )}
      </Drawer>

      <Drawer
        opened={inviting}
        onClose={() => setInviting(false)}
        position="right"
        size="md"
        title={<span className="text-[14px] font-semibold">Invite people</span>}
      >
        <div className="mb-3 flex items-baseline justify-between gap-3">
          <p className="text-[12px] text-ink-muted">{candidates.length} on the roster can be invited</p>
          {candidates.length > 0 && (
            <button
              onClick={() => setPicked(picked.length === candidates.length ? [] : candidates.map((c) => c.id))}
              className="text-[11.5px] font-medium"
              style={{ color: 'var(--color-s1)' }}
            >
              {picked.length === candidates.length ? 'Clear' : 'Select all'}
            </button>
          )}
        </div>
          <div className="mb-2.5 flex flex-wrap items-center gap-1.5">
            {(['all', ...groups] as string[]).map((g) => (
              <button
                key={g}
                onClick={() => setGroup(g)}
                className="hair rounded-md bg-surface px-2 py-1 text-[11.5px] font-medium"
                style={group === g ? { background: 'var(--color-wash)', color: 'var(--color-brand-text)' } : { color: 'var(--color-ink-2)' }}
              >
                {g === 'all' ? 'All' : g}
              </button>
            ))}
          </div>
          <TextInput
            size="xs"
            placeholder="Search roster"
            leftSection={<IconSearch size={13} stroke={1.8} />}
            value={q}
            onChange={(e) => setQ(e.currentTarget.value)}
          />

          <div className="scroll-thin mt-2.5 flex max-h-[calc(100dvh-360px)] min-h-[160px] flex-col gap-1 overflow-y-auto">
            {candidates.length === 0 && (
              <p className="py-6 text-center text-[12.5px] text-ink-muted">
                {people.length === 0 ? 'Roster is empty.' : 'Everyone matching already has an open invitation.'}
              </p>
            )}
            {candidates.map((p) => {
              const on = picked.includes(p.id)
              return (
                <button
                  key={p.id}
                  onClick={() => setPicked(on ? picked.filter((x) => x !== p.id) : [...picked, p.id])}
                  aria-pressed={on}
                  aria-label={`Select ${p.name}`}
                  className="flex items-center gap-2.5 rounded-lg border px-2.5 py-2 text-left"
                  style={on ? { borderColor: 'transparent', background: 'var(--color-wash)' } : { borderColor: 'var(--color-hair)' }}
                >
                  <span
                    className="grid h-4.5 w-4.5 shrink-0 place-items-center rounded"
                    style={on ? { background: 'var(--color-s1)' } : { boxShadow: 'inset 0 0 0 1px var(--color-rule)' }}
                  >
                    {on && <IconCheck size={11} stroke={3} color="#fff" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[12.5px] font-medium text-ink">{p.name}</span>
                    <span className="tnum block truncate text-[11px] text-ink-muted">{p.email}</span>
                    {lastOf.has(p.id) && (
                      <span className="block truncate text-[11px]" style={{ color: 'var(--color-brand-text)' }}>
                        Sitting {lastOf.get(p.id)!.sitting + 1} · last one {SEEN_LABEL[seen(lastOf.get(p.id)!)].toLowerCase()}
                      </span>
                    )}
                  </span>
                  {p.group && <Badge size="xs" variant="default">{p.group}</Badge>}
                </button>
              )
            })}
          </div>

          {versions.length > 1 && (
            <Select
              size="xs"
              label="Version"
              description="Earlier versions stay available for a new sitting."
              className="mt-3"
              allowDeselect={false}
              value={pickVersion}
              onChange={(v) => setPickVersion(v ?? '')}
              data={versions.map((v) => ({
                value: v.version === exam.publishedVersion ? '' : String(v.version),
                label: `v${v.version}${v.version === exam.publishedVersion ? ' · live' : ''} · published ${when(v.publishedAt)}`,
              }))}
            />
          )}

          <TextInput
            size="xs"
            type="date"
            label="Due date"
            description="Optional."
            className="mt-3"
            value={due}
            onChange={(e) => setDue(e.currentTarget.value)}
          />

          <button
            onClick={async () => {
              const r = await assign(exam.id, picked, dueAt, examVersion)
              invited(invitedNote(r, live), r.invited > 0)
              setPicked([])
            }}
            disabled={picked.length === 0}
            className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg py-2.5 text-[13px] font-semibold text-on-brand disabled:opacity-40"
            style={{ background: 'var(--color-s1)' }}
          >
            <IconSend size={15} stroke={2} />
            Invite {picked.length > 0 ? `${picked.length} ` : ''}
            {picked.length === 1 ? 'person' : 'people'}
          </button>
          {note && (
            <p role="status" className="mt-2 text-[12px] text-ink-2">
              {note}
            </p>
          )}
          <div className="mt-4 border-t border-hair pt-3">
            <button
              onClick={() => setAdhocOpen((o) => !o)}
              className="inline-flex items-center gap-1.5 text-[12.5px] font-medium"
              style={{ color: 'var(--color-s1)' }}
            >
              <IconMailPlus size={14} stroke={2} /> Invite someone not on the roster
            </button>
            <Collapse expanded={adhocOpen} keepMounted={false}>
              <Textarea
                className="mt-2.5"
                size="xs"
                autosize
                minRows={3}
                placeholder={'dewi@example.id\nAgus Wibowo <agus@example.id>\nSiti Rahayu, siti@example.id, QA'}
                value={adhoc}
                onChange={(e) => setAdhoc(e.currentTarget.value)}
              />
              <p className="mt-1.5 text-[11px] leading-snug text-ink-muted">
                One per line. New addresses are added to the roster too — an invitation needs a
                person to hang status and score on.
              </p>
              <button
                onClick={async () => {
                  const r = await assignByEmail(exam.id, adhoc, dueAt, examVersion)
                  invited(invitedNote(r, live), r.invited > 0)
                  if (r.invited) setAdhoc('')
                }}
                disabled={!adhoc.trim()}
                className="mt-2.5 flex w-full items-center justify-center gap-1.5 rounded-lg py-2 text-[12.5px] font-semibold text-on-brand disabled:opacity-40"
                style={{ background: 'var(--color-s1)' }}
              >
                <IconSend size={14} stroke={2} /> Invite by email
              </button>
              <div className="mt-4 border-t border-hair pt-3">
                <RosterImport
                  roster={people}
                  includeKnown
                  action="Invite"
                  onConfirm={async (drafts) => {
                    const r = await assignDrafts(exam.id, drafts, dueAt, examVersion)
                    invited(invitedNote(r, live), r.invited > 0)
                  }}
                />
              </div>
            </Collapse>
          </div>
      </Drawer>
    </div>
  )
}
