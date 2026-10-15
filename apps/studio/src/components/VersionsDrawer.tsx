import { useEffect, useState } from 'react'
import { Drawer } from '@mantine/core'
import { IconChevronDown, IconRestore } from '@tabler/icons-react'
import { useApp } from '../state'
import { type Exam, type ExamVersion, allItems } from '../store'

const day = (t: number) =>
  new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })

// Notes the system writes on every snapshot say nothing a reader needs.
const QUIET_NOTES = new Set(['published', 'backfilled'])

/**
 * Every frozen version of an instrument, newest first. Each can be read, and
 * any but the working copy can be brought back as the working draft. Nothing
 * here changes what candidates get: that still takes a publish.
 */
export function VersionsDrawer({ exam, onClose }: { exam: Exam | undefined; onClose: () => void }) {
  const listVersions = useApp((s) => s.listVersions)
  const saveExam = useApp((s) => s.saveExam)
  const openInEditor = useApp((s) => s.openInEditor)
  const [versions, setVersions] = useState<ExamVersion[] | null>(null)
  const [reading, setReading] = useState<number | null>(null)
  const [restoring, setRestoring] = useState<number | null>(null)

  const id = exam?.id
  useEffect(() => {
    setVersions(null)
    setReading(null)
    setRestoring(null)
    if (!id) return
    let live = true
    listVersions(id).then((vs) => live && setVersions(vs))
    return () => {
      live = false
    }
  }, [id, listVersions])

  // The working copy has moved ahead of what candidates get.
  const ahead = !!exam && exam.publishedVersion !== undefined && exam.version > exam.publishedVersion

  const restore = async (v: ExamVersion) => {
    if (!exam) return
    // Content from the old version; identity and lifecycle stay the instrument's own.
    const next: Exam = {
      ...v.exam,
      id: exam.id,
      kind: exam.kind,
      status: exam.status,
      version: exam.version,
      publishedVersion: exam.publishedVersion,
      updatedAt: exam.updatedAt,
    }
    // Never over the live version: branch a new working one unless one is already ahead.
    const saved = await saveExam(next, ahead ? 'draft' : 'new')
    if (saved) {
      onClose()
      openInEditor(saved.id)
    }
  }

  return (
    <Drawer
      opened={!!exam}
      onClose={onClose}
      position="right"
      size="md"
      title={<span className="text-[14px] font-semibold">Versions · {exam?.title}</span>}
    >
      {exam && (
        <p className="mb-3 text-[12px] leading-snug text-ink-muted">
          Candidates get <strong className="font-medium text-ink">v{exam.publishedVersion}</strong>.
          {ahead && ` You are editing v${exam.version}, which is not published yet.`} Every published
          version is kept, and people who sat one stay on it.
        </p>
      )}
      {versions === null ? (
        <p className="py-6 text-center text-[12.5px] text-ink-muted">Loading versions…</p>
      ) : versions.length === 0 ? (
        <p className="py-6 text-center text-[12.5px] text-ink-muted">Nothing has been published yet.</p>
      ) : (
        <ol className="flex flex-col gap-2" aria-label="Published versions">
          {versions.map((v) => {
            const isLive = v.version === exam?.publishedVersion
            const isWorking = v.version === exam?.version
            const items = allItems(v.exam)
            return (
              <li key={v.version} className="hair rounded-xl bg-surface p-3.5">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="flex items-center gap-2 text-[13.5px] font-semibold text-ink">
                      v{v.version}
                      {isLive && (
                        <span
                          className="rounded-full px-2 py-0.5 text-[10.5px] font-semibold uppercase tracking-wide"
                          style={{ background: 'var(--color-wash-good)', color: 'var(--color-good-text)' }}
                        >
                          Live
                        </span>
                      )}
                    </p>
                    <p className="tnum mt-0.5 text-[11.5px] text-ink-muted">
                      Published {day(v.publishedAt)} · {items.length} questions
                      {v.note && !QUIET_NOTES.has(v.note) ? ` · ${v.note}` : ''}
                    </p>
                    {v.exam.title !== exam?.title && <p className="mt-0.5 truncate text-[11.5px] text-ink-2">“{v.exam.title}”</p>}
                  </div>
                  <div className="flex shrink-0 items-center gap-1.5">
                    <button
                      onClick={() => setReading(reading === v.version ? null : v.version)}
                      aria-expanded={reading === v.version}
                      aria-label={`Questions in v${v.version}`}
                      className="hair inline-flex items-center gap-1 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium text-ink-2"
                    >
                      Questions
                      <IconChevronDown size={12} stroke={2} style={{ transform: reading === v.version ? 'rotate(180deg)' : undefined }} />
                    </button>
                    {!isWorking && (
                      <button
                        onClick={() => setRestoring(v.version)}
                        aria-label={`Restore v${v.version} as draft`}
                        className="hair inline-flex items-center gap-1 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium"
                        style={{ color: 'var(--color-brand-text)' }}
                      >
                        <IconRestore size={12} stroke={2} /> Restore
                      </button>
                    )}
                  </div>
                </div>

                {restoring === v.version && exam && (
                  <div className="mt-3 rounded-lg p-3 text-[12px] leading-snug" style={{ background: 'var(--color-sunken)' }}>
                    <p className="text-ink-2">
                      v{v.version}'s questions and settings become the working draft
                      {ahead ? ` v${exam.version}, replacing its unpublished changes` : ` v${exam.version + 1}`}. Candidates
                      keep getting v{exam.publishedVersion} until you publish it.
                    </p>
                    <div className="mt-2.5 flex gap-2">
                      <button
                        onClick={() => restore(v)}
                        className="rounded-md px-3 py-1.5 text-[12px] font-semibold text-on-brand"
                        style={{ background: 'var(--color-s1)' }}
                      >
                        Restore as draft
                      </button>
                      <button onClick={() => setRestoring(null)} className="hair rounded-md bg-surface px-3 py-1.5 text-[12px] font-medium text-ink-2">
                        Cancel
                      </button>
                    </div>
                  </div>
                )}

                {reading === v.version && (
                  <ol className="mt-3 flex flex-col gap-1.5 border-t border-hair pt-3">
                    {items.map((it, n) => (
                      <li key={it.id} className="text-[12.5px] leading-snug text-ink">
                        <span className="tnum text-ink-muted">{n + 1}.</span> {it.stem || 'Untitled question'}
                        {it.choices.length > 0 && (
                          <span className="mt-0.5 block text-[11.5px] text-ink-muted">
                            {it.choices.map((c) => `${it.correct.includes(c.id) ? '✓ ' : ''}${c.text || c.id}`).join(' · ')}
                          </span>
                        )}
                      </li>
                    ))}
                  </ol>
                )}
              </li>
            )
          })}
        </ol>
      )}
    </Drawer>
  )
}
