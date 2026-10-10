import { Fragment, useMemo, useState } from 'react'
import { Select, TextInput, Textarea, Badge, Tooltip, Collapse } from '@mantine/core'
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
} from '@tabler/icons-react'
import { useApp } from '../state'
import { Panel } from '../components/Panel'
import { RosterImport } from '../components/RosterImport'
import { type LinkState, EVENT_LABEL, linkState, listOutbox } from '../invite'
import { StatTile } from '../components/Viz'
import {
  type AssignStatus,
  type Assignment,
  type Exam,
  ASSIGN_STATUS,
  KINDS,
  allItems,
  isScored,
} from '../store'

const STATUS_STYLE: Record<AssignStatus, { bg: string; fg: string }> = {
  invited: { bg: 'var(--color-sunken)', fg: 'var(--color-ink-2)' },
  started: { bg: 'var(--color-wash)', fg: 'var(--color-brand-text)' },
  submitted: { bg: 'var(--color-wash-good)', fg: 'var(--color-good-text)' },
  expired: { bg: 'var(--color-wash-warn)', fg: 'var(--color-serious)' },
}

function StatusPill({ status }: { status: AssignStatus }) {
  const s = STATUS_STYLE[status]
  return (
    <span
      className="inline-flex items-center rounded px-1.5 py-0.5 text-[10.5px] font-semibold uppercase tracking-wide"
      style={{ background: s.bg, color: s.fg }}
    >
      {ASSIGN_STATUS.find((x) => x.v === status)?.label}
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
  const currentExamId = useApp((s) => s.currentExamId)
  const setScreen = useApp((s) => s.setScreen)

  const active = useMemo(() => exams.filter((e) => e.status !== 'archived'), [exams])
  const [examId, setExamId] = useState<string>(
    () => currentExamId ?? active[0]?.id ?? '',
  )
  const exam: Exam | undefined = active.find((e) => e.id === examId) ?? active[0]

  const [picked, setPicked] = useState<string[]>([])
  const [group, setGroup] = useState('all')
  const [q, setQ] = useState('')
  const [due, setDue] = useState('')
  const [note, setNote] = useState('')
  const [historyOf, setHistoryOf] = useState('')
  const [adhocOpen, setAdhocOpen] = useState(false)
  const [adhoc, setAdhoc] = useState('')

  const rows = useMemo(
    () => (exam ? assignments.filter((a) => a.examId === exam.id) : []),
    [assignments, exam],
  )
  const assignedIds = new Set(rows.map((r) => r.personId))
  const personOf = (id: string) => people.find((p) => p.id === id)

  const groups = useMemo(() => [...new Set(people.map((p) => p.group).filter(Boolean))].sort(), [people])
  const candidates = useMemo(
    () =>
      people
        .filter((p) => !assignedIds.has(p.id))
        .filter((p) => group === 'all' || p.group === group)
        .filter((p) => {
          const t = q.trim().toLowerCase()
          return !t || p.name.toLowerCase().includes(t) || p.email.toLowerCase().includes(t)
        }),
    [people, assignedIds, group, q],
  )

  const live = exam?.status === 'published'
  const dueAt = due ? new Date(due).getTime() : undefined
  const unsent = rows.filter((r) => linkState(r) === 'unsent' && r.status !== 'submitted')
  // Every send writes an assignment, so this re-reads exactly when there is something new.
  const mails = useMemo(() => {
    const mine = new Set(rows.map((r) => r.id))
    return listOutbox().filter((m) => mine.has(m.assignmentId)).slice(0, 12)
  }, [rows])

  const resend = async (a: Assignment, name: string) => {
    const n = await sendInvite([a.id])
    setNote(n ? `New link emailed to ${name}. The previous one no longer works.` : 'Nothing sent — publish the instrument first.')
  }

  const count = (s: AssignStatus) => rows.filter((r) => r.status === s).length

  if (!exam)
    return (
      <div className="p-6">
        <p className="hair rounded-xl bg-surface py-12 text-center text-[13px] text-ink-muted">
          Nothing to assign yet — create an instrument in the Library first.
        </p>
      </div>
    )

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-4">
        <h1 className="text-[15px] font-semibold tracking-tight">Assignments</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Give named people a way in. Each assignment mints its own invitation link — one person,
          one token, never a shared URL — and tracks them from invited through to submitted.
        </p>
      </header>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Select
          size="xs"
          label="Instrument"
          className="min-w-[280px] flex-1"
          value={exam.id}
          allowDeselect={false}
          onChange={(v) => v && setExamId(v)}
          data={active.map((e) => ({
            value: e.id,
            label: `${e.title} — ${KINDS.find((k) => k.v === e.kind)?.label}${e.status === 'draft' ? ' (draft)' : ''}`,
          }))}
        />
        <button
          onClick={() => setScreen('people')}
          className="hair inline-flex h-[30px] items-center gap-1.5 rounded-lg bg-surface px-3 text-[12.5px] font-medium text-ink-2"
        >
          <IconUsers size={14} stroke={1.9} /> Manage roster
        </button>
      </div>

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

      <div className="mb-4 grid grid-cols-2 gap-3 xl:grid-cols-4">
        <StatTile label="Assigned" value={String(rows.length)} />
        <StatTile label="Not started" value={String(count('invited'))} />
        <StatTile label="In progress" value={String(count('started'))} />
        <StatTile label="Submitted" value={String(count('submitted'))} />
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,340px)_minmax(0,1fr)]">
        <Panel
          title="Assign people"
          hint={`${candidates.length} on the roster not yet assigned`}
          actions={
            candidates.length > 0 && (
              <button
                onClick={() => setPicked(picked.length === candidates.length ? [] : candidates.map((c) => c.id))}
                className="text-[11.5px] font-medium"
                style={{ color: 'var(--color-s1)' }}
              >
                {picked.length === candidates.length ? 'Clear' : 'Select all'}
              </button>
            )
          }
        >
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

          <div className="scroll-thin mt-2.5 flex max-h-[260px] flex-col gap-1 overflow-y-auto">
            {candidates.length === 0 && (
              <p className="py-6 text-center text-[12.5px] text-ink-muted">
                {people.length === 0 ? 'Roster is empty.' : 'Everyone matching is already assigned.'}
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
                  </span>
                  {p.group && <Badge size="xs" variant="default">{p.group}</Badge>}
                </button>
              )
            })}
          </div>

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
              setNote(invitedNote(await assign(exam.id, picked, dueAt), live))
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
                  const r = await assignByEmail(exam.id, adhoc, dueAt)
                  setNote(invitedNote(r, live))
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
                  onConfirm={async (drafts) => setNote(invitedNote(await assignDrafts(exam.id, drafts, dueAt), live))}
                />
              </div>
            </Collapse>
          </div>
        </Panel>

        <Panel
          title="Invited"
          hint={rows.length === 0 ? 'No one assigned yet.' : `${rows.length} assigned · ${allItems(exam).length} questions`}
          actions={
            live && unsent.length > 0 && (
              <button
                onClick={async () => {
                  const n = await sendInvite(unsent.map((r) => r.id))
                  setNote(`${n} held ${n === 1 ? 'invitation' : 'invitations'} emailed.`)
                }}
                className="hair inline-flex items-center gap-1.5 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium text-ink-2"
              >
                <IconSend size={12} stroke={1.9} /> Send {unsent.length} held
              </button>
            )
          }
          pad={rows.length === 0}
        >
          {rows.length === 0 ? (
            <p className="py-10 text-center text-[12.5px] text-ink-muted">
              Pick people on the left and invite them. Each is emailed their own link.
            </p>
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
                  {rows.map((r) => {
                    const p = personOf(r.personId)
                    const name = p?.name ?? 'Unknown'
                    const state = linkState(r)
                    const cols = isScored(exam) ? 7 : 6
                    return (
                      <Fragment key={r.id}>
                      <tr className="border-b border-hair/60 last:border-0">
                        <td className="px-4 py-3 sm:px-5">
                          <p className="text-[13px] font-medium text-ink">{p?.name ?? 'Unknown'}</p>
                          <p className="tnum text-[11px] text-ink-muted">{p?.email}</p>
                        </td>
                        <td className="px-3 py-3">
                          <StatusPill status={r.status} />
                          <LinkLine a={r} />
                        </td>
                        <td className="tnum px-3 py-3 text-[12px] text-ink-muted">v{r.examVersion}</td>
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
                              <button onClick={() => unassignOne(r.id)} aria-label={`Unassign ${p?.name}`} className="hair grid h-7 w-7 place-items-center rounded-md bg-surface">
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
              {rows.map((r) => {
                const p = personOf(r.personId)
                return (
                  <div key={r.id} className="hair rounded-xl bg-surface p-3.5">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="truncate text-[13.5px] font-medium text-ink">{p?.name}</p>
                        <p className="tnum truncate text-[11.5px] text-ink-muted">{p?.email}</p>
                      </div>
                      <StatusPill status={r.status} />
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
      </div>

      <Panel
        className="mt-4"
        title={
          <span className="inline-flex items-center gap-1.5">
            <IconInbox size={14} stroke={1.9} className="text-ink-muted" /> Outbox — prototype stand-in for email
          </span>
        }
        hint="No mail is sent yet. This lists what would have gone out, and it is the only place a link or a code can be read: the assignment keeps a hash, not the link. With a mail provider this panel goes away."
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
    </div>
  )
}
