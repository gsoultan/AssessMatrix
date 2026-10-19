/**
 * The proctor monitor against the core: every sitting of one exam, live over
 * ProctorService.WatchExam, and one attempt's record (GetAttemptLog) with the
 * flags it raised and the reason for each, in words the candidate could check.
 */
import { useEffect, useMemo, useState } from 'react'
import { Badge, Select } from '@mantine/core'
import { IconAlertTriangle, IconShieldCheck, IconShieldX } from '@tabler/icons-react'
import type { Sitting } from '@assessmatrix/proto/proctor_pb'
import { AssignStatus } from '@assessmatrix/proto/assignment_pb'
import type { Flag, GetAttemptLogResponse } from '@assessmatrix/proto/result_pb'
import type { AttemptEvent } from '@assessmatrix/proto/candidate_pb'
import { Panel } from '../components/Panel'
import { StatTile } from '../components/Viz'
import { api } from '../api/client'
import { explain } from '../backend/repo'
import { useApp } from '../state'
import { isLive } from '../store'

const SEV: Record<string, { color: string; rank: number; word: string }> = {
  serious: { color: 'var(--color-critical)', rank: 3, word: 'Serious' },
  review: { color: 'var(--color-serious)', rank: 2, word: 'Review' },
  info: { color: 'var(--color-ink-muted)', rank: 1, word: 'Note' },
}
const rank = (s: Sitting) => Math.max(0, ...s.flags.map((f) => SEV[f.severity]?.rank ?? 0))

type Link = 'connecting' | 'live' | 'retrying'

/** Watch one exam until unmounted. The server ends each stream after a few minutes; we reconnect, which re-checks the session. */
function useSittings(examId: string) {
  const [sittings, setSittings] = useState<Sitting[] | null>(null)
  const [link, setLink] = useState<Link>('connecting')
  const [error, setError] = useState('')
  useEffect(() => {
    if (!examId) return
    const stop = new AbortController()
    setSittings(null)
    setLink('connecting')
    void (async () => {
      let wait = 1000
      while (!stop.signal.aborted) {
        try {
          for await (const m of api.proctor.watchExam({ examId }, { signal: stop.signal })) {
            setSittings(m.sittings)
            setLink('live')
            setError('')
            wait = 1000
          }
        } catch (err) {
          if (stop.signal.aborted) return
          setError(explain(err))
          setLink('retrying')
          await new Promise((r) => setTimeout(r, wait))
          wait = Math.min(wait * 2, 30_000)
        }
      }
    })()
    return () => stop.abort()
  }, [examId])
  return { sittings, link, error }
}

export function LiveMonitor() {
  const exams = useApp((s) => s.exams)
  const live = useMemo(() => exams.filter(isLive), [exams])
  const [examId, setExamId] = useState('')
  useEffect(() => {
    if (!examId && live[0]) setExamId(live[0].id)
  }, [examId, live])
  const { sittings, link, error } = useSittings(examId)
  const [open, setOpen] = useState<Sitting | null>(null)
  const now = Date.now()

  const sorted = useMemo(
    () =>
      [...(sittings ?? [])].sort(
        (a, b) => rank(b) - rank(a) || Number(b.startedAt) - Number(a.startedAt) || a.personName.localeCompare(b.personName),
      ),
    [sittings],
  )
  const inProgress = sorted.filter((s) => s.attemptId && !s.submittedAt).length
  const handedIn = sorted.filter((s) => s.submittedAt).length
  const flagged = sorted.filter((s) => rank(s) >= 2).length

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">Proctor monitor</h1>
          <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
            Every sitting of one assessment, live. Every flag states what was observed, so a candidate who contests it
            can be answered with the record.
          </p>
        </div>
        <span
          className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1"
          style={{ background: link === 'live' ? 'var(--color-wash-good)' : 'var(--color-wash)' }}
        >
          <span
            className={`h-1.5 w-1.5 rounded-full ${link === 'live' ? 'pulse-dot' : ''}`}
            style={{ background: link === 'live' ? 'var(--color-good)' : 'var(--color-ink-muted)' }}
          />
          <span className="text-[11.5px] font-medium" style={{ color: link === 'live' ? 'var(--color-good-text)' : undefined }}>
            {!examId ? 'Nothing to watch' : link === 'live' ? `Live · ${inProgress} in progress` : link === 'retrying' ? 'Reconnecting…' : 'Connecting…'}
          </span>
        </span>
      </header>

      {live.length === 0 ? (
        <Panel title="Nothing is live">
          <p className="text-[13px] text-ink-2">Publish an assessment and assign people to it; their sittings show here as they happen.</p>
        </Panel>
      ) : (
        <>
          <div className="mb-4 max-w-[420px]">
            <Select
              label="Assessment"
              data={live.map((e) => ({ value: e.id, label: e.title || 'Untitled' }))}
              value={examId}
              onChange={(v) => {
                setExamId(v ?? '')
                setOpen(null)
              }}
              allowDeselect={false}
            />
          </div>
          {error && link === 'retrying' && (
            <p role="status" className="mb-3 text-[12.5px]" style={{ color: 'var(--color-serious)' }}>
              {error}
            </p>
          )}
          <div className="mb-5 grid grid-cols-2 gap-3 xl:grid-cols-4">
            <StatTile label="In progress" value={String(inProgress)} />
            <StatTile label="Handed in" value={String(handedIn)} />
            <StatTile label="Flagged for review" value={String(flagged)} />
            <StatTile label="Not started" value={String(sorted.length - inProgress - handedIn)} />
          </div>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
            <Panel title="Sittings" hint="Most serious flags first." pad={false}>
              {sittings === null ? (
                <p className="px-5 py-4 text-[12.5px] text-ink-muted">Loading…</p>
              ) : sorted.length === 0 ? (
                <p className="px-5 py-4 text-[12.5px] text-ink-muted">No one is assigned this assessment yet.</p>
              ) : (
                <ul aria-label="Sittings">
                  {sorted.map((s) => (
                    <li key={s.assignmentId} className="border-b border-hair/60 last:border-0">
                      <button
                        disabled={!s.attemptId}
                        onClick={() => setOpen(s)}
                        aria-current={open?.assignmentId === s.assignmentId}
                        className="block w-full px-4 py-3 text-left enabled:hover:bg-wash disabled:cursor-default sm:px-5"
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0">
                            <p className="truncate text-[13.5px] font-medium text-ink">{s.personName}</p>
                            <p className="tnum truncate text-[11.5px] text-ink-muted">{describe(s, now)}</p>
                          </div>
                          {s.total > 0 && s.attemptId && (
                            <span className="tnum shrink-0 text-[11.5px] text-ink-muted">
                              {s.answered}/{s.total} answered
                            </span>
                          )}
                        </div>
                        {s.flags.length > 0 && (
                          <div className="mt-2 flex flex-wrap gap-1.5">
                            {s.flags.map((f, i) => (
                              <Badge key={i} size="xs" variant="outline" radius="sm" style={{ color: SEV[f.severity]?.color, borderColor: 'currentColor' }}>
                                {label(f)}
                              </Badge>
                            ))}
                          </div>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </Panel>
            {open?.attemptId ? (
              <AttemptRecord key={open.attemptId} sitting={open} />
            ) : (
              <Panel title="Attempt record">
                <p className="text-[12.5px] text-ink-muted">Choose a sitting to see its flags, their reasons and the full record.</p>
              </Panel>
            )}
          </div>
        </>
      )}
    </div>
  )
}

function describe(s: Sitting, now: number) {
  if (s.status === AssignStatus.EXPIRED && !s.attemptId) return 'Invitation expired'
  if (s.submittedAt) return `Handed in ${clock(Number(s.submittedAt))}${s.scorePct ? ` · ${s.scorePct}%` : ''}`
  if (!s.attemptId) return 'Not started'
  const seen = Number(s.lastSeenAt)
  const quiet = seen ? Math.round((now - seen) / 60000) : 0
  return `Started ${clock(Number(s.startedAt))}${quiet >= 2 ? ` · last heard from ${quiet} min ago (answers keep on the device)` : ''}`
}

const clock = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

const label = (f: Flag) => f.type.replaceAll('_', ' ')

/** One attempt's record: is it intact, what did the rules find and why, and every event in order. */
function AttemptRecord({ sitting }: { sitting: Sitting }) {
  const [log, setLog] = useState<GetAttemptLogResponse | null>(null)
  const [error, setError] = useState('')
  // Refetch whenever the live view says this attempt changed.
  const changed = `${sitting.answered}:${Number(sitting.lastSeenAt)}:${sitting.flags.length}`
  useEffect(() => {
    let stale = false
    api.results.getAttemptLog({ attemptId: sitting.attemptId }).then(
      (r) => !stale && setLog(r),
      (err) => !stale && setError(explain(err)),
    )
    return () => {
      stale = true
    }
  }, [sitting.attemptId, changed])

  return (
    <Panel title={`Attempt record — ${sitting.personName}`} hint="The attempt's event log, sealed event by event. This is the record a decision rests on.">
      {error ? (
        <p className="text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
          {error}
        </p>
      ) : !log ? (
        <p className="text-[12.5px] text-ink-muted">Loading…</p>
      ) : (
        <>
          {log.purgedAt > 0n ? (
            <p className="mb-3 text-[12.5px] text-ink-2">
              Record deleted on {new Date(Number(log.purgedAt)).toLocaleDateString()} at the end of its retention period. The score stands.
            </p>
          ) : (
            <p className="mb-3 flex items-center gap-1.5 text-[12.5px]" style={{ color: log.chainIntact ? 'var(--color-good-text)' : 'var(--color-critical)' }}>
              {log.chainIntact ? <IconShieldCheck size={15} stroke={1.9} /> : <IconShieldX size={15} stroke={1.9} />}
              {log.chainIntact ? 'Record intact: every event matches its seal.' : `Record altered from event ${log.brokenAtSeq}.`}
            </p>
          )}
          {log.flags.length > 0 ? (
            <ul aria-label="Flags" className="mb-4 flex flex-col gap-2">
              {log.flags.map((f, i) => (
                <li key={i} className="rounded-lg px-3 py-2.5" style={{ background: 'var(--color-wash)' }}>
                  <p className="flex items-center gap-1.5 text-[12.5px] font-medium" style={{ color: SEV[f.severity]?.color }}>
                    <IconAlertTriangle size={14} stroke={2} /> {SEV[f.severity]?.word ?? f.severity} · {label(f)}
                  </p>
                  <p className="mt-1 text-[12.5px] leading-snug text-ink-2">{f.reason}</p>
                  <p className="mt-1 text-[11.5px] leading-snug text-ink-muted" lang="id">
                    {f.reasonId}
                  </p>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mb-4 text-[12.5px] text-ink-muted">No flags. Nothing in this record calls for a closer look.</p>
          )}
          <ol aria-label="Events" className="relative flex max-h-[420px] flex-col overflow-auto border-l border-hair pl-4">
            {log.events.map((e) => (
              <li key={String(e.seq)} className="relative pb-3 last:pb-0">
                <span className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full" style={{ background: dot(e), boxShadow: '0 0 0 2px var(--color-surface)' }} />
                <p className="tnum text-[11px] text-ink-muted">
                  #{String(e.seq)} · {new Date(Number(e.serverTime)).toLocaleTimeString()}
                </p>
                <p className="mt-0.5 text-[12.5px] leading-snug text-ink-2">{say(e)}</p>
              </li>
            ))}
          </ol>
        </>
      )}
    </Panel>
  )
}

const dot = (e: AttemptEvent) =>
  e.kind.startsWith('vision.') || e.kind === 'answer.pasted' || e.kind === 'session.second_tab'
    ? 'var(--color-serious)'
    : e.kind.startsWith('focus.') || e.kind === 'fullscreen.exited' || e.kind.endsWith('.withdrawn')
      ? 'var(--color-warn)'
      : e.kind.startsWith('attempt.') || e.kind.startsWith('consent.')
        ? 'var(--color-good)'
        : 'var(--color-ink-muted)'

/** The runner's codes for why the camera stopped (services/core domain.CameraStopped). */
const CAMERA_STOPPED: Record<string, string> = {
  permission_refused: 'camera permission was refused',
  no_camera: 'no camera could be opened',
  camera_ended: 'the camera was turned off or disconnected',
  check_failed: 'the face check failed on this device',
  check_unavailable: 'the face check could not load on this device',
}

const secs = (v?: string) => `${Math.round(Number(v ?? 0) / 1000)} s`

/** An event in a sentence. Numbers come from values; nothing here interprets them beyond units. */
function say(e: AttemptEvent): string {
  const v = e.values
  switch (e.kind) {
    case 'focus.lost':
      return `Left the exam (${e.detail || 'out of view'})`
    case 'focus.returned':
      return `Came back after ${secs(v[0])}`
    case 'answer.pasted':
      return `Pasted ${v[0]} characters into ${e.itemId}`
    case 'item.viewed':
      return `On ${e.itemId} for ${secs(v[0])}`
    case 'device.seen':
      return `Device: ${v[1] ?? 'unknown'} (${v[0]})`
    case 'session.second_tab':
      return 'A second tab tried to open this sitting and was refused'
    case 'fullscreen.exited':
      return 'Left full screen'
    case 'vision.face_missing':
      return `No face in view for ${secs(v[0])}`
    case 'vision.faces_multiple':
      return `${v[1] ?? 'Several'} faces in view for ${secs(v[0])}`
    case 'vision.stopped':
      return `Camera check stopped: ${CAMERA_STOPPED[e.detail ?? ''] ?? 'no recognised reason'}`
    case 'response.changed':
      return `Answered ${e.itemId}`
    case 'response.typed':
      return `Typed an answer to ${e.itemId}`
    default:
      if (e.kind.startsWith('consent.') && e.kind.endsWith('.withdrawn'))
        return `Withdrew consent: ${e.kind.slice('consent.'.length, -'.withdrawn'.length)}`
      if (e.kind.startsWith('consent.')) return `Agreed to: ${e.kind.slice('consent.'.length)}`
      return e.detail ? `${e.kind} · ${e.detail}` : e.kind
  }
}
