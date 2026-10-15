import { useEffect, useState } from 'react'
import { Checkbox, Modal } from '@mantine/core'
import { IconShare } from '@tabler/icons-react'
import type { Exam } from '../store'
import { type ExamCopy, type ShareResult, tenancy } from '../backend/tenancy'
import { explain } from '../backend/repo'

/** Where a tenant's copy stands against the version about to be shared. */
function standing(c: ExamCopy, live: number): string {
  if (!c.examId) return 'Not shared yet'
  const held = `Has version ${c.sourceVersion}`
  if (c.archived) return `${held} · archived there`
  if (!c.publishedVersion) return `${held} · taken offline there`
  return c.sourceVersion === live ? `${held} · up to date` : `${held} · version ${live} not rolled out`
}

/**
 * Roll the live version of an instrument out to other tenants you administer.
 * Each chosen tenant gets its own copy, published there; one that already has
 * a copy gets a new version of it, and invitations already sent there keep
 * the version they were sent with.
 */
export function ShareDialog({ exam, onClose }: { exam: Exam | null; onClose: () => void }) {
  const [copies, setCopies] = useState<ExamCopy[] | null>(null)
  const [chosen, setChosen] = useState<Set<string>>(new Set())
  const [results, setResults] = useState<Record<string, ShareResult>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const live = exam?.publishedVersion ?? 0

  const load = async (id: string) => {
    try {
      setCopies(await tenancy.copies(id))
    } catch (err) {
      setError(explain(err))
    }
  }
  useEffect(() => {
    setCopies(null)
    setChosen(new Set())
    setResults({})
    setError('')
    if (exam) void load(exam.id)
  }, [exam?.id])

  const share = async () => {
    if (!exam || chosen.size === 0) return
    setBusy(true)
    setError('')
    try {
      const rs = await tenancy.share(exam.id, live, [...chosen])
      setResults(Object.fromEntries(rs.map((r) => [r.tenantId, r])))
      setChosen(new Set(rs.filter((r) => r.error).map((r) => r.tenantId)))
      await load(exam.id)
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }

  const toggle = (id: string, on: boolean) =>
    setChosen((s) => {
      const n = new Set(s)
      if (on) n.add(id)
      else n.delete(id)
      return n
    })

  return (
    <Modal opened={!!exam} onClose={onClose} title={exam ? `Share “${exam.title}”` : ''} centered size="md">
      {!live ? (
        <p className="text-[12.5px] text-ink-muted">Publish it first: only a published version can be shared.</p>
      ) : (
        <>
          <p className="mb-3 text-[12.5px] leading-snug text-ink-muted">
            Shares version {live}, the one live now. Each tenant you pick gets it published as its own copy, with its
            own invitations and results. Invitations already sent there keep the version they were sent with.
          </p>
          {copies === null && !error && (
            <p role="status" className="text-[12.5px] text-ink-muted">
              Loading…
            </p>
          )}
          {copies?.length === 0 && (
            <p className="text-[12.5px] text-ink-muted">
              You administer no other tenant. Create one from the tenant menu, or ask an administrator of another
              tenant to make you one there.
            </p>
          )}
          {copies && copies.length > 0 && (
            <ul className="hair flex flex-col rounded-lg">
              {copies.map((c) => {
                const r = results[c.tenantId]
                const current = c.examId && c.sourceVersion === live && c.publishedVersion > 0
                return (
                  <li key={c.tenantId} className="border-b border-hair/60 px-3 py-2.5 last:border-0">
                    <Checkbox
                      size="xs"
                      checked={chosen.has(c.tenantId)}
                      disabled={busy || !!current}
                      onChange={(e) => toggle(c.tenantId, e.currentTarget.checked)}
                      label={<span className="text-[13px] font-medium text-ink">{c.tenantName}</span>}
                      description={standing(c, live)}
                    />
                    {r?.error && (
                      <p role="alert" className="mt-1 pl-7 text-[12px] text-ink">
                        {r.error}
                      </p>
                    )}
                    {r && !r.error && <p className="mt-1 pl-7 text-[12px] text-ink-2">Shared and published there.</p>}
                  </li>
                )
              })}
            </ul>
          )}
          {error && (
            <p role="alert" className="mt-3 text-[12.5px] text-ink">
              {error}
            </p>
          )}
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={onClose} className="px-3 py-2 text-[12.5px] font-medium text-ink-2">
              Close
            </button>
            <button
              onClick={() => void share()}
              disabled={busy || chosen.size === 0}
              className="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-[12.5px] font-semibold text-on-brand disabled:opacity-60"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconShare size={14} stroke={2} />
              {chosen.size === 0
                ? 'Choose tenants'
                : `Share version ${live} with ${chosen.size} ${chosen.size === 1 ? 'tenant' : 'tenants'}`}
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}
