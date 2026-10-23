import { IconCheck, IconX, IconUserCheck } from '@tabler/icons-react'
import { ItemImage } from './ItemImage'
import { type Attempt, type Exam, type Item, allItems, earned, isScale, isScored, penalty } from '../store'

type Verdict = 'right' | 'partial' | 'wrong' | 'blank' | 'person' | 'none'

/** The same rule `score()` applies, one item at a time, so the sheet and the total agree. */
function verdict(exam: Exam, it: Item, a: Attempt): Verdict {
  const chosen = a.answers[it.id] ?? []
  const text = (a.text[it.id] ?? '').trim()
  if (!isScored(exam) || isScale(it.type)) return chosen.length || text ? 'none' : 'blank'
  if (it.type === 'essay') return text ? 'person' : 'blank'
  if (it.type === 'short' ? !text : chosen.length === 0) return 'blank'
  const got = earned(it, a)
  return got >= it.points ? 'right' : got > 0 ? 'partial' : 'wrong'
}

const LOOK: Record<Verdict, { label: string; color: string; bg: string }> = {
  right: { label: 'Correct', color: 'var(--color-good-text)', bg: 'var(--color-wash-good)' },
  partial: { label: 'Partly right', color: 'var(--color-serious)', bg: 'var(--color-wash-warn)' },
  wrong: { label: 'Wrong', color: 'var(--color-critical)', bg: 'var(--color-wash-critical)' },
  blank: { label: 'Not answered', color: 'var(--color-ink-muted)', bg: 'var(--color-sunken)' },
  person: { label: 'Marked by a person', color: 'var(--color-serious)', bg: 'var(--color-wash-warn)' },
  none: { label: '', color: '', bg: '' },
}

const choiceText = (it: Item, id: string) => it.choices.find((c) => c.id === id)?.text || id

/**
 * One candidate's submitted answers, question by question, against the key of
 * the version they sat. Read-only: marking essays happens in Scoring, blind.
 */
export function AnswerSheet({ exam, attempt }: { exam: Exam; attempt: Attempt }) {
  const items = allItems(exam)
  return (
    <ol className="flex flex-col gap-2.5">
      {items.map((it, n) => {
        const v = verdict(exam, it, attempt)
        const look = LOOK[v]
        const chosen = attempt.answers[it.id] ?? []
        const text = attempt.text[it.id]?.trim()
        const file = attempt.files?.[it.id]
        const scored = v === 'right' || v === 'partial' || v === 'wrong' || (v === 'blank' && isScored(exam) && !isScale(it.type))
        return (
          <li key={it.id} className="hair rounded-xl bg-surface p-4">
            <div className="flex items-start justify-between gap-3">
              <p className="min-w-0 text-[13.5px] font-medium leading-snug text-ink">
                <span className="tnum text-ink-muted">{n + 1}.</span> {it.stem || 'Untitled question'}
              </p>
              {look.label && (
                <span
                  className="inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold"
                  style={{ color: look.color, background: look.bg }}
                >
                  {v === 'right' && <IconCheck size={12} stroke={2.6} />}
                  {v === 'wrong' && <IconX size={12} stroke={2.6} />}
                  {v === 'person' && <IconUserCheck size={12} stroke={2.2} />}
                  {look.label}
                </span>
              )}
            </div>

            <div className="mt-2 text-[13px] leading-snug text-ink-2">
              {chosen.length > 0 && (
                <p>
                  <span className="text-ink-muted">Answered: </span>
                  <strong className="font-medium text-ink">
                    {isScale(it.type) ? chosen.join(', ') : chosen.map((c) => choiceText(it, c)).join(', ')}
                  </strong>
                </p>
              )}
              {text && <p className="whitespace-pre-wrap rounded-lg p-2.5 text-ink" style={{ background: 'var(--color-sunken)' }}>{text}</p>}
              {file && <ItemImage media={file} max={220} className="mt-2 w-fit" />}
              {v === 'blank' && <p className="text-ink-muted">No answer.</p>}
              {(v === 'wrong' || v === 'partial') && it.correct.length > 0 && (
                <p className="mt-1">
                  <span className="text-ink-muted">Correct answer: </span>
                  {it.type === 'short' ? it.correct.join(' / ') : it.correct.map((c) => choiceText(it, c)).join(', ')}
                </p>
              )}
            </div>

            {scored && (
              <p className="tnum mt-2 text-[11.5px] text-ink-muted">
                {earned(it, attempt) - penalty(it, attempt)} / {it.points} points
                {penalty(it, attempt) > 0 && ' (wrong-answer penalty)'}
              </p>
            )}
          </li>
        )
      })}
    </ol>
  )
}
