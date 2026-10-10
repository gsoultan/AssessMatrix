import { useRef, useState } from 'react'
import { IconFileSpreadsheet, IconCheck, IconAlertTriangle } from '@tabler/icons-react'
import type { Person, PersonDraft } from '../store'
import { type RosterReport, RosterFileError, readRosterFile, rowsToRoster } from '../roster'

/**
 * Pick a CSV or Excel file, see exactly what it would do, then decide.
 *
 * Nothing is written until the confirm button: a roster file is somebody
 * else's export, and the first anyone should learn that 40 of its rows had no
 * usable address is before those people silently miss their invitation.
 */
export function RosterImport({
  roster,
  includeKnown = false,
  action,
  onConfirm,
}: {
  roster: Person[]
  /** Invite people the file names who are already on the roster, rather than skipping them. */
  includeKnown?: boolean
  /** The verb on the confirm button: "Add" or "Invite". */
  action: string
  onConfirm: (people: PersonDraft[]) => void | Promise<void>
}) {
  const input = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState('')
  const [report, setReport] = useState<RosterReport | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const reset = () => {
    setReport(null)
    setFile('')
    setError('')
    if (input.current) input.current.value = ''
  }

  const pick = async (f: File | undefined) => {
    if (!f) return
    setError('')
    setReport(null)
    setFile(f.name)
    try {
      setReport(rowsToRoster(await readRosterFile(f), roster))
    } catch (e) {
      // Only our own messages are shown; anything else is a bug, not advice.
      setError(e instanceof RosterFileError ? e.message : 'That file could not be read.')
    }
  }

  const chosen = report ? (includeKnown ? [...report.fresh, ...report.known] : report.fresh) : []

  return (
    <div>
      <label className="hair inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-surface px-3 py-2 text-[12.5px] font-medium text-ink-2 focus-within:outline focus-within:outline-2">
        <IconFileSpreadsheet size={14} stroke={1.9} />
        Choose a CSV or Excel file
        <input
          ref={input}
          type="file"
          accept=".csv,.tsv,.txt,.xlsx"
          aria-label="Upload a CSV or Excel roster"
          className="sr-only"
          onChange={(e) => pick(e.currentTarget.files?.[0])}
        />
      </label>
      <p className="mt-1.5 text-[11px] leading-snug text-ink-muted">
        A header row with <span className="text-ink-2">name</span>, <span className="text-ink-2">email</span> and{' '}
        <span className="text-ink-2">group</span> columns, in any order. Only those three are read — any other column
        in the file is ignored and never stored. Up to 5,000 rows.
      </p>

      {error && (
        <p role="alert" className="mt-2.5 flex items-start gap-1.5 text-[12px] leading-snug" style={{ color: 'var(--color-critical)' }}>
          <IconAlertTriangle size={14} stroke={2.1} className="mt-px shrink-0" /> {error}
        </p>
      )}

      {report && (
        <div className="mt-3 rounded-lg p-3" style={{ background: 'var(--color-sunken)' }} data-testid="roster-report">
          <p className="tnum text-[12.5px] font-medium text-ink">{file}</p>
          <ul className="tnum mt-1.5 flex flex-col gap-0.5 text-[12px] text-ink-2">
            <li>{report.fresh.length} new</li>
            {report.known.length > 0 && (
              <li>
                {report.known.length} already on the roster — {includeKnown ? 'invited too, not added twice' : 'skipped'}
              </li>
            )}
            {report.repeated > 0 && <li>{report.repeated} repeated in the file — counted once</li>}
            {report.rejected.length > 0 && (
              <li style={{ color: 'var(--color-critical)' }}>{report.rejected.length} rows cannot be used</li>
            )}
          </ul>

          {report.rejected.length > 0 && (
            <ul className="tnum mt-2 flex flex-col gap-0.5 border-t border-hair pt-2 text-[11.5px] text-ink-2">
              {report.rejected.slice(0, 8).map((r) => (
                <li key={r.row}>
                  Row {r.row}: {r.reason}
                  {r.shown && <span className="text-ink-muted"> — {r.shown}</span>}
                </li>
              ))}
              {report.rejected.length > 8 && <li className="text-ink-muted">and {report.rejected.length - 8} more</li>}
            </ul>
          )}

          <div className="mt-3 flex items-center gap-2">
            <button
              disabled={chosen.length === 0 || busy}
              onClick={async () => {
                setBusy(true)
                try {
                  await onConfirm(chosen)
                  reset()
                } finally {
                  setBusy(false)
                }
              }}
              className="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-[12.5px] font-semibold text-on-brand disabled:opacity-40"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconCheck size={14} stroke={2.2} /> {action} {chosen.length} {chosen.length === 1 ? 'person' : 'people'}
            </button>
            <button onClick={reset} className="text-[12.5px] font-medium text-ink-2">
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
