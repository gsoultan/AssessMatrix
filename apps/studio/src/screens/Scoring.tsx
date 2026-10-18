/**
 * Scoring against the core: essays waiting for a person, marked blind, and
 * the item analysis the psychometrics worker computed for the exam.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Badge, NumberInput, SegmentedControl, Select, Textarea } from '@mantine/core'
import type { GetItemAnalysisResponse, ReviewTask } from '@assessmatrix/proto/result_pb'
import { Panel } from '../components/Panel'
import { StatTile } from '../components/Viz'
import { ItemImage } from '../components/ItemImage'
import { api } from '../api/client'
import { explain } from '../backend/repo'
import { useApp } from '../state'
import { isLive, isScored } from '../store'
import { mediaIn } from '../backend/convert'

export function Scoring() {
  const exams = useApp((s) => s.exams)
  const scored = useMemo(() => exams.filter((e) => isLive(e) && isScored(e)), [exams])
  const [examId, setExamId] = useState('')
  const [tab, setTab] = useState<'review' | 'analysis'>('review')
  useEffect(() => {
    if (!examId && scored[0]) setExamId(scored[0].id)
  }, [examId, scored])

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Scoring</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Essays are scored by people, blind to who wrote them. Item analysis shows which questions are doing their job.
        </p>
      </header>
      {scored.length === 0 ? (
        <Panel title="Nothing to score">
          <p className="text-[13px] text-ink-2">Publish a scored exam; its hand-ins show here.</p>
        </Panel>
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-end gap-3">
            <Select
              className="w-full max-w-[420px]"
              label="Exam"
              data={scored.map((e) => ({ value: e.id, label: e.title || 'Untitled' }))}
              value={examId}
              onChange={(v) => setExamId(v ?? '')}
              allowDeselect={false}
            />
            <SegmentedControl
              value={tab}
              onChange={(v) => setTab(v as typeof tab)}
              data={[
                { value: 'review', label: 'Essay review' },
                { value: 'analysis', label: 'Item analysis' },
              ]}
            />
          </div>
          {examId && (tab === 'review' ? <Review key={examId} examId={examId} /> : <Analysis key={examId} examId={examId} />)}
        </>
      )}
    </div>
  )
}

function Review({ examId }: { examId: string }) {
  const [tasks, setTasks] = useState<ReviewTask[] | null>(null)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    api.results.listReviews({ examId, includeDone: done }).then(
      (r) => setTasks(r.tasks),
      (err) => setError(explain(err)),
    )
  }, [examId, done])
  useEffect(load, [load])

  const waiting = tasks?.filter((t) => !t.reviewed).length ?? 0
  return (
    <>
      <div className="mb-4 grid grid-cols-2 gap-3 xl:grid-cols-4">
        <StatTile label="Essays waiting" value={tasks ? String(waiting) : '…'} />
      </div>
      <Panel
        title="Essays"
        hint="Labelled by hand-in order, not by name. Your note is shown to the candidate with the score."
        actions={
          <label className="flex items-center gap-2 text-[12.5px] text-ink-2">
            <input type="checkbox" checked={done} onChange={(e) => setDone(e.currentTarget.checked)} /> Show scored
          </label>
        }
      >
        {error ? (
          <p className="text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
            {error}
          </p>
        ) : !tasks ? (
          <p className="text-[12.5px] text-ink-muted">Loading…</p>
        ) : tasks.length === 0 ? (
          <p className="text-[12.5px] text-ink-muted">Nothing waiting. Every essay handed in has a score.</p>
        ) : (
          <ul aria-label="Essays to score" className="flex flex-col gap-4">
            {tasks.map((t) => (
              <Task key={`${t.attemptId}:${t.itemId}`} task={t} onSaved={load} />
            ))}
          </ul>
        )}
      </Panel>
    </>
  )
}

function Task({ task, onSaved }: { task: ReviewTask; onSaved: () => void }) {
  const [points, setPoints] = useState<number | string>(task.reviewed ? task.given : '')
  const [note, setNote] = useState(task.note)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const file = task.file ? mediaIn(task.file) : undefined
  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await api.results.scoreAnswer({ attemptId: task.attemptId, itemId: task.itemId, points: Number(points), note })
      onSaved()
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <li className="rounded-xl border border-hair p-4" aria-label={`${task.label}, question ${task.number}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-[13px] font-medium text-ink">
          {task.label} · Question {task.number}
        </p>
        {task.reviewed && (
          <Badge size="sm" variant="light">
            Scored {task.given}/{task.points}
          </Badge>
        )}
      </div>
      <p className="mt-1 text-[12.5px] text-ink-muted">{task.stem}</p>
      <div className="mt-3 whitespace-pre-wrap rounded-lg px-3 py-2.5 text-[13.5px] leading-relaxed text-ink" style={{ background: 'var(--color-wash)' }}>
        {task.text || <span className="text-ink-muted">No answer written.</span>}
      </div>
      {file && <ItemImage media={file} max={320} className="mt-3 w-fit" />}
      <div className="mt-3 flex flex-wrap items-end gap-3">
        <NumberInput
          label={`Points (0–${task.points})`}
          min={0}
          max={task.points}
          clampBehavior="strict"
          allowDecimal={false}
          value={points}
          onChange={setPoints}
          className="w-[150px]"
        />
        <Textarea label="Note to the candidate" autosize minRows={1} maxLength={2000} value={note} onChange={(e) => setNote(e.currentTarget.value)} className="min-w-[240px] flex-1" />
        <button
          onClick={save}
          disabled={busy || points === '' || Number(points) < 0 || Number(points) > task.points}
          className="h-9 rounded-lg px-4 text-[13px] font-medium text-white disabled:opacity-50"
          style={{ background: 'var(--color-s1)' }}
        >
          {task.reviewed ? 'Update score' : 'Save score'}
        </button>
      </div>
      {error && (
        <p className="mt-2 text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
          {error}
        </p>
      )}
    </li>
  )
}

const fmt = (x: number) => x.toFixed(2)

function Analysis({ examId }: { examId: string }) {
  const [a, setA] = useState<GetItemAnalysisResponse | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let stale = false
    const load = () =>
      api.results.getItemAnalysis({ examId }).then(
        (r) => !stale && setA(r),
        (err) => !stale && setError(explain(err)),
      )
    void load()
    // The worker runs on its own; look again while it is catching up.
    const t = window.setInterval(load, 5000)
    return () => {
      stale = true
      window.clearInterval(t)
    }
  }, [examId])

  if (error)
    return (
      <p className="text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
        {error}
      </p>
    )
  if (!a) return <p className="text-[12.5px] text-ink-muted">Loading…</p>
  if (a.items.length === 0)
    return (
      <Panel title="Item analysis">
        <p className="text-[12.5px] text-ink-muted">
          {a.pending ? 'Being computed from the hand-ins…' : 'No hand-ins to analyse yet.'}
        </p>
      </Panel>
    )
  return (
    <>
      <div className="mb-4 grid grid-cols-2 gap-3 xl:grid-cols-4">
        <StatTile label="Fully scored hand-ins" value={String(a.n)} />
        <StatTile label="Reliability (KR-20)" value={fmt(a.kr20)} />
        <StatTile label="Version" value={`v${a.examVersion}`} />
      </div>
      {(a.tooFew || a.pending) && (
        <p role="status" className="mb-3 text-[12.5px] text-ink-2">
          {a.tooFew && `Provisional: ${a.n} fully scored hand-ins; below 30 these numbers move a lot with each new one. `}
          {a.pending && 'Newer hand-ins are being added.'}
        </p>
      )}
      <Panel
        title="Questions"
        hint="Difficulty is the share of full marks (higher is easier). Discrimination is the correlation with the rest of the exam; under 0.20 deserves a look, negative usually means a wrong key."
        pad={false}
      >
        <table className="w-full text-left text-[12.5px]" aria-label="Item analysis">
          <thead className="text-ink-muted">
            <tr className="border-b border-hair">
              <th className="px-4 py-2 font-medium">Question</th>
              <th className="px-2 py-2 font-medium">Difficulty</th>
              <th className="px-2 py-2 font-medium">Discrimination</th>
              <th className="px-4 py-2 font-medium">Options (all · top 27% · bottom 27%)</th>
            </tr>
          </thead>
          <tbody>
            {a.items.map((it) => (
              <tr key={it.itemId} className="border-b border-hair/60 align-top last:border-0">
                <td className="px-4 py-2.5 text-ink">
                  Q{it.number}
                  {it.notes.map((n, i) => (
                    <p key={i} className="mt-1 max-w-[46ch] text-[11.5px] leading-snug" style={{ color: 'var(--color-serious)' }}>
                      {n}
                    </p>
                  ))}
                </td>
                <td className="tnum px-2 py-2.5">{fmt(it.pValue)}</td>
                <td
                  className="tnum px-2 py-2.5"
                  style={{ color: !it.noSpread && it.pointBiserial < 0.2 ? 'var(--color-serious)' : undefined }}
                >
                  {it.noSpread ? '—' : fmt(it.pointBiserial)}
                </td>
                <td className="tnum px-4 py-2.5 text-ink-2">
                  {it.choices.map((c, i) => (
                    <span key={c.choiceId} className="mr-3 inline-block">
                      <strong className={c.correct ? 'text-ink' : 'font-normal'}>{'ABCDEFGHIJ'[i] ?? i + 1}</strong>
                      {c.correct ? '✓' : ''} {Math.round(c.share * 100)}% · {Math.round(c.shareUpper * 100)}% · {Math.round(c.shareLower * 100)}%
                    </span>
                  ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </>
  )
}
