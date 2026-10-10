import { useMemo, useState } from 'react'
import { Badge } from '@mantine/core'
import { IconTrash, IconInbox, IconMessage2 } from '@tabler/icons-react'
import { useApp } from '../state'
import { Panel } from '../components/Panel'
import { StatTile } from '../components/Viz'
import {
  type Exam,
  aggregate,
  allItems,
  isScale,
  isScored,
  listResponses,
  KINDS,
} from '../store'

/**
 * What "results" means when nothing is graded: a distribution, not a score.
 * One hue for every bar — the options are categories of a single series, and
 * colouring them differently would imply an identity encoding that isn't there.
 */
function Bar({ label, n, total }: { label: string; n: number; total: number }) {
  const pct = total === 0 ? 0 : Math.round((n / total) * 100)
  return (
    <div className="flex items-center gap-3 py-1">
      <span className="w-[42%] shrink-0 truncate text-[12.5px] text-ink-2 sm:w-[34%]">{label}</span>
      <div className="h-2.5 min-w-0 flex-1 rounded-full" style={{ background: 'var(--color-track)' }}>
        <div className="h-full rounded-full" style={{ width: `${pct}%`, background: 'var(--color-s1)' }} />
      </div>
      <span className="tnum w-[68px] shrink-0 text-right text-[12px] text-ink">
        {n} · {pct}%
      </span>
    </div>
  )
}

export function Responses() {
  const exams = useApp((s) => s.exams)
  const byExam = useApp((s) => s.responses)
  const clearFor = useApp((s) => s.clearResponsesFor)
  const [exam, setExam] = useState<Exam>(
    () => useApp.getState().exams.find((e) => e.kind === 'survey') ?? useApp.getState().exams[0]!,
  )

  // Fall back to storage for instruments not submitted to in this session.
  const countFor = (id: string) => (byExam[id] ?? listResponses(id)).length
  const responses = useMemo(() => byExam[exam.id] ?? listResponses(exam.id), [byExam, exam])
  const rows = useMemo(() => aggregate(exam, responses), [exam, responses])
  const items = allItems(exam)

  const completion =
    responses.length === 0
      ? 0
      : Math.round(
          (rows.reduce((n, r) => n + r.n, 0) / Math.max(1, responses.length * items.length)) * 100,
        )

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Responses</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Aggregate view for questionnaires. Each response is a submitted attempt, so the same
          delivery engine that runs a proctored exam also runs an anonymous survey.
        </p>
      </header>

      <div className="mb-4 flex flex-wrap gap-2">
        {exams.map((e) => (
          <button
            key={e.id}
            onClick={() => setExam(e)}
            className="hair inline-flex items-center gap-2 rounded-lg bg-surface px-3 py-1.5 text-[12.5px] font-medium"
            style={e.id === exam.id ? { background: 'var(--color-wash)', color: 'var(--color-brand-text)' } : { color: 'var(--color-ink-2)' }}
          >
            {e.title}
            <Badge size="xs" variant="default">
              {countFor(e.id)}
            </Badge>
          </button>
        ))}
      </div>

      {responses.length === 0 ? (
        <Panel title={exam.title} hint={KINDS.find((k) => k.v === exam.kind)?.hint}>
          <div className="py-10 text-center">
            <IconInbox size={28} stroke={1.5} className="mx-auto text-ink-muted" />
            <p className="mt-2.5 text-[13px] text-ink-2">No responses yet.</p>
            <p className="mt-1 text-[12px] text-ink-muted">
              Open <strong className="font-medium text-ink-2">Take exam</strong>, fill this in, and submit —
              then come back. Each submission lands here.
            </p>
          </div>
        </Panel>
      ) : (
        <>
          <div className="mb-4 grid grid-cols-2 gap-3 xl:grid-cols-4">
            <StatTile label="Responses" value={String(responses.length)} />
            <StatTile label="Questions" value={String(items.length)} />
            <StatTile label="Item completion" value={`${completion}%`} />
            <StatTile label="Anonymous" value={exam.anonymous ? 'Yes' : 'No'} />
          </div>

          <div className="flex flex-col gap-3">
            {rows.map((r, n) => {
              const it = r.item
              const scaleStart = it.type === 'rating' && (it.scale?.points ?? 0) > 7 ? 0 : 1
              return (
                <Panel
                  key={it.id}
                  title={
                    <span className="flex items-start gap-2">
                      <span className="tnum shrink-0 text-ink-muted">{n + 1}.</span>
                      <span className="min-w-0">{it.stem || <em className="text-ink-muted">Untitled</em>}</span>
                    </span>
                  }
                  hint={
                    <>
                      {r.n} of {responses.length} answered
                      {r.mean !== undefined && (
                        <>
                          {' · '}
                          <strong className="font-medium text-ink">mean {r.mean.toFixed(1)}</strong>
                        </>
                      )}
                    </>
                  }
                >
                  {isScale(it.type) && it.scale ? (
                    <div className="flex flex-col">
                      {Array.from({ length: it.scale.points }, (_, i) => {
                        const v = String(scaleStart + i)
                        return <Bar key={v} label={v} n={r.counts[v] ?? 0} total={r.n} />
                      })}
                      <div className="mt-1.5 flex justify-between text-[11px] text-ink-muted">
                        <span>{it.scale.minLabel}</span>
                        <span>{it.scale.maxLabel}</span>
                      </div>
                    </div>
                  ) : it.choices.length > 0 ? (
                    <div className="flex flex-col">
                      {it.choices.map((c) => (
                        <Bar key={c.id} label={c.text || '—'} n={r.counts[c.id] ?? 0} total={r.n} />
                      ))}
                    </div>
                  ) : r.texts.length > 0 ? (
                    <ul className="flex flex-col gap-1.5">
                      {r.texts.slice(0, 12).map((t, i) => (
                        <li key={i} className="flex items-start gap-2 rounded-lg border border-hair px-3 py-2">
                          <IconMessage2 size={13} stroke={1.8} className="mt-0.5 shrink-0 text-ink-muted" />
                          <span className="text-[12.5px] leading-snug text-ink-2">{t}</span>
                        </li>
                      ))}
                      {r.texts.length > 12 && (
                        <li className="tnum text-[11.5px] text-ink-muted">+{r.texts.length - 12} more</li>
                      )}
                    </ul>
                  ) : (
                    <p className="text-[12.5px] text-ink-muted">No free-text answers yet.</p>
                  )}

                  {isScored(exam) && it.correct.length > 0 && (
                    <p className="mt-2.5 border-t border-hair pt-2 text-[11.5px] text-ink-muted">
                      This item has an answer key — in a graded instrument the distribution doubles as
                      item analysis.
                    </p>
                  )}
                </Panel>
              )
            })}
          </div>

          <button
            onClick={() => clearFor(exam.id)}
            className="hair mt-4 inline-flex items-center gap-1.5 rounded-lg bg-surface px-3 py-2 text-[12.5px] font-medium text-ink-2"
          >
            <IconTrash size={14} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
            Clear {responses.length} response{responses.length === 1 ? '' : 's'}
          </button>
        </>
      )}
    </div>
  )
}
