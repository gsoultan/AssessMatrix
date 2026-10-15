import { useMemo, useRef, useState } from 'react'
import { Badge, Menu, TextInput } from '@mantine/core'
import {
  IconSearch,
  IconPlus,
  IconDots,
  IconPencil,
  IconEye,
  IconCopy,
  IconTrash,
  IconFileCheck,
  IconBolt,
  IconClipboardText,
  IconInbox,
  IconChartBar,
  IconSend,
  IconArchive,
  IconArchiveOff,
  IconRocket,
  IconPencilMinus,
  IconFileImport,
  IconFileExport,
  IconVersions,
  IconShare,
} from '@tabler/icons-react'
import { useApp, useResponseCount } from '../state'
import { VersionsDrawer } from '../components/VersionsDrawer'
import { CORE } from '../backend/mode'
import { ShareDialog } from '../components/ShareDialog'
import {
  type Exam,
  type Kind,
  type Status,
  KINDS,
  allItems,
  isScored,
  hasUnpublishedVersion,
  totalMinutes,
  totalPoints,
} from '../store'

const KIND_ICON: Record<Kind, typeof IconFileCheck> = {
  exam: IconFileCheck,
  quiz: IconBolt,
  survey: IconClipboardText,
}
const KIND_COLOR: Record<Kind, string> = { exam: 'red', quiz: 'blue', survey: 'teal' }

const STATUS_STYLE: Record<Status, { label: string; bg: string; fg: string }> = {
  draft: { label: 'Draft', bg: 'var(--color-sunken)', fg: 'var(--color-ink-2)' },
  published: { label: 'Published', bg: 'var(--color-wash-good)', fg: 'var(--color-good-text)' },
  archived: { label: 'Archived', bg: 'var(--color-wash-warn)', fg: 'var(--color-serious)' },
}

function StatusPill({ status }: { status: Status }) {
  const s = STATUS_STYLE[status]
  return (
    <span
      className="inline-flex items-center rounded px-1.5 py-0.5 text-[10.5px] font-semibold uppercase tracking-wide"
      style={{ background: s.bg, color: s.fg }}
    >
      {s.label}
    </span>
  )
}

function ago(ts: number) {
  const s = Math.max(0, Math.round((Date.now() - ts) / 1000))
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  if (s < 604800) return `${Math.floor(s / 86400)}d ago`
  return new Date(ts).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
}

export function Library({ isDesktop }: { isDesktop: boolean }) {
  const exams = useApp((s) => s.exams)
    const createExam = useApp((s) => s.createExam)
  const duplicateExam = useApp((s) => s.duplicateExam)
  const removeExam = useApp((s) => s.removeExam)
  const destroyExam = useApp((s) => s.destroyExam)
  const setStatus = useApp((s) => s.setStatus)
  const openInEditor = useApp((s) => s.openInEditor)
  const previewAsCandidate = useApp((s) => s.previewAsCandidate)
  const openAssignments = useApp((s) => s.openAssignments)
  const setScreen = useApp((s) => s.setScreen)
  const importExam = useApp((s) => s.importExam)
  const exportExam = useApp((s) => s.exportExam)
  // Sharing to other tenants: the core, and its administrators, only.
  const role = useApp((s) => s.staff?.role)
  const canShare = CORE && role === 'admin'
  const [sharing, setSharing] = useState<Exam | null>(null)

  const [kind, setKind] = useState<Kind | 'all'>('all')
  const [view, setView] = useState<'active' | 'archived'>('active')
  const [q, setQ] = useState('')
  const [versionsOf, setVersionsOf] = useState('')

  const inView = (e: Exam) => (view === 'archived' ? e.status === 'archived' : e.status !== 'archived')
  const archivedCount = exams.filter((e) => e.status === 'archived').length

  const countFor = useResponseCount()

  const shown = useMemo(
    () =>
      exams
        .filter(inView)
        .filter((e) => kind === 'all' || e.kind === kind)
        .filter((e) => {
          const t = q.trim().toLowerCase()
          return !t || e.title.toLowerCase().includes(t) || e.issuer.toLowerCase().includes(t)
        })
        .sort((a, b) => b.updatedAt - a.updatedAt),
    [exams, kind, q, view],
  )

  // Outside the menu: the menu unmounts its items as it closes, before a file is picked.
  const qtiInput = useRef<HTMLInputElement>(null)
  const qtiPicker = (
    <input
      ref={qtiInput}
      type="file"
      accept=".zip,application/zip"
      hidden
      aria-label="QTI package"
      onChange={async (ev) => {
        const f = ev.currentTarget.files?.[0]
        ev.currentTarget.value = ''
        if (!f) return
        const e = await importExam(f)
        if (e) openInEditor(e.id)
      }}
    />
  )

  const newMenu = (
    <Menu position="bottom-end" width={250}>
      <Menu.Target>
        <button
          className="inline-flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-2 text-[13px] font-semibold text-on-brand"
          style={{ background: 'var(--color-s1)' }}
        >
          <IconPlus size={15} stroke={2.4} /> New
        </button>
      </Menu.Target>
      <Menu.Dropdown>
        {KINDS.map((k) => {
          const Icon = KIND_ICON[k.v]
          return (
            <Menu.Item
              key={k.v}
              leftSection={<Icon size={15} stroke={1.8} />}
              onClick={async () => {
                const e = await createExam(k.v)
                if (e) openInEditor(e.id)
              }}
            >
              <span className="block text-[12.5px]">{k.label}</span>
              <span className="block text-[11px] leading-snug text-ink-muted">{k.hint}</span>
            </Menu.Item>
          )
        })}
        <Menu.Divider />
        <Menu.Item leftSection={<IconFileImport size={15} stroke={1.8} />} onClick={() => qtiInput.current?.click()}>
          <span className="block text-[12.5px]">Import QTI package</span>
          <span className="block text-[11px] leading-snug text-ink-muted">A .zip from a QTI 2.1 or 3.0 tool.</span>
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  )

  const actions = (e: Exam) => (
    <Menu position="bottom-end" width={190}>
      <Menu.Target>
        <button className="hair grid h-7 w-7 shrink-0 place-items-center rounded-md bg-surface" aria-label={`Actions for ${e.title}`}>
          <IconDots size={14} stroke={2} className="text-ink-2" />
        </button>
      </Menu.Target>
      <Menu.Dropdown>
        {e.status === 'archived' ? (
          <>
            <Menu.Item leftSection={<IconArchiveOff size={14} stroke={1.9} />} onClick={() => setStatus(e.id, 'draft')}>
              Restore as draft
            </Menu.Item>
            <Menu.Item
              leftSection={<IconCopy size={14} stroke={1.9} />}
              onClick={async () => {
                const copy = await duplicateExam(e.id)
                if (copy) openInEditor(copy.id)
              }}
            >
              Duplicate
            </Menu.Item>
            <Menu.Item leftSection={<IconFileExport size={14} stroke={1.9} />} onClick={() => exportExam(e.id)}>
              Export QTI
            </Menu.Item>
            <Menu.Divider />
            <Menu.Item color="red" leftSection={<IconTrash size={14} stroke={1.9} />} onClick={() => destroyExam(e.id)}>
              Delete permanently
            </Menu.Item>
          </>
        ) : (
          <>
            <Menu.Item leftSection={<IconPencil size={14} stroke={1.9} />} onClick={() => openInEditor(e.id)}>
              Edit
            </Menu.Item>
            <Menu.Item leftSection={<IconEye size={14} stroke={1.9} />} onClick={() => previewAsCandidate(e.id)}>
              Preview as candidate
            </Menu.Item>
            <Menu.Item leftSection={<IconSend size={14} stroke={1.9} />} onClick={() => openAssignments(e.id)}>
              Assign people
            </Menu.Item>
            {e.publishedVersion !== undefined && (
              <Menu.Item leftSection={<IconVersions size={14} stroke={1.9} />} onClick={() => setVersionsOf(e.id)}>
                Versions
              </Menu.Item>
            )}
            {e.status === 'draft' ? (
              <Menu.Item
                leftSection={<IconRocket size={14} stroke={1.9} />}
                disabled={allItems(e).length === 0}
                onClick={() => setStatus(e.id, 'published')}
              >
                Publish
              </Menu.Item>
            ) : (
              <Menu.Item leftSection={<IconPencilMinus size={14} stroke={1.9} />} onClick={() => setStatus(e.id, 'draft')}>
                Revert to draft
              </Menu.Item>
            )}
            {e.kind === 'survey' && (
              <Menu.Item leftSection={<IconChartBar size={14} stroke={1.9} />} onClick={() => setScreen('responses')}>
                View responses
              </Menu.Item>
            )}
            <Menu.Item
              leftSection={<IconCopy size={14} stroke={1.9} />}
              onClick={async () => {
                const copy = await duplicateExam(e.id)
                if (copy) openInEditor(copy.id)
              }}
            >
              Duplicate
            </Menu.Item>
            <Menu.Item leftSection={<IconFileExport size={14} stroke={1.9} />} onClick={() => exportExam(e.id)}>
              Export QTI
            </Menu.Item>
            {canShare && (
              <Menu.Item
                leftSection={<IconShare size={14} stroke={1.9} />}
                disabled={!e.publishedVersion}
                onClick={() => setSharing(e)}
              >
                Share with tenants
              </Menu.Item>
            )}
            <Menu.Divider />
            <Menu.Item leftSection={<IconArchive size={14} stroke={1.9} />} onClick={() => removeExam(e.id)}>
              Archive
            </Menu.Item>
          </>
        )}
      </Menu.Dropdown>
    </Menu>
  )

  const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

  const meta = (e: Exam) =>
    [
      hasUnpublishedVersion(e)
        ? `v${e.publishedVersion} live · v${e.version} draft`
        : `v${e.version}`,
      plural(allItems(e).length, 'question'),
      isScored(e) ? plural(totalPoints(e), 'pt') : null,
      e.timed ? `${totalMinutes(e)} min` : 'untimed',
      e.kind === 'exam' ? `Tier ${e.tier}` : null,
      e.anonymous ? 'anonymous' : null,
      e.sharedFrom ? `from ${e.sharedFrom.tenantName} v${e.sharedFrom.version}` : null,
    ]
      .filter(Boolean)
      .join(' · ')

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">Library</h1>
          <p className="mt-0.5 max-w-[68ch] text-[12.5px] leading-snug text-ink-muted">
            Everything you have built — exams, quizzes and questionnaires in one place. Open one to
            edit it, or preview it exactly as a participant would see it.
          </p>
        </div>
        {newMenu}
        {qtiPicker}
        <VersionsDrawer exam={exams.find((x) => x.id === versionsOf)} onClose={() => setVersionsOf('')} />
      </header>
      {canShare && <ShareDialog exam={sharing} onClose={() => setSharing(null)} />}

      <div className="mb-3 flex items-center gap-2">
        <div className="hair inline-flex rounded-lg bg-surface p-0.5">
          {(['active', 'archived'] as const).map((v) => (
            <button
              key={v}
              onClick={() => setView(v)}
              className="inline-flex items-center gap-1.5 rounded-[6px] px-3 py-1.5 text-[12.5px] font-medium capitalize"
              style={
                view === v
                  ? { background: 'var(--color-s1)', color: 'var(--color-on-brand)' }
                  : { color: 'var(--color-ink-muted)' }
              }
            >
              {v === 'archived' && <IconArchive size={13} stroke={1.9} />}
              {v}
              {v === 'archived' && archivedCount > 0 && <span className="tnum opacity-70">{archivedCount}</span>}
            </button>
          ))}
        </div>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        {([['all', 'All'], ...KINDS.map((k) => [k.v, `${k.label}s`])] as [Kind | 'all', string][]).map(
          ([v, label]) => {
            const on = kind === v
            const pool = exams.filter(inView)
            const n = v === 'all' ? pool.length : pool.filter((e) => e.kind === v).length
            return (
              <button
                key={v}
                onClick={() => setKind(v)}
                className="hair inline-flex items-center gap-1.5 rounded-lg bg-surface px-3 py-1.5 text-[12.5px] font-medium"
                style={on ? { background: 'var(--color-wash)', color: 'var(--color-brand-text)' } : { color: 'var(--color-ink-2)' }}
              >
                {label}
                <span className="tnum text-[11px] text-ink-muted">{n}</span>
              </button>
            )
          },
        )}
        <TextInput
          size="xs"
          className="ml-auto w-full sm:w-[220px]"
          placeholder="Search by title or issuer"
          leftSection={<IconSearch size={14} stroke={1.8} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
      </div>

      {shown.length === 0 ? (
        <div className="hair rounded-xl bg-surface py-14 text-center">
          <IconInbox size={30} stroke={1.4} className="mx-auto text-ink-muted" />
          <p className="mt-3 text-[13.5px] font-medium text-ink">
            {view === 'archived' ? 'Nothing archived' : exams.length === 0 ? 'Nothing here yet' : 'No matches'}
          </p>
          <p className="mt-1 text-[12.5px] text-ink-muted">
            {view === 'archived'
              ? 'Archiving retires an instrument without destroying the work behind it.'
              : exams.length === 0
                ? 'Create an exam, a quiz or a questionnaire to get started.'
                : 'Try a different filter or search term.'}
          </p>
          {exams.length === 0 && view === 'active' && <div className="mt-4 flex justify-center">{newMenu}</div>}
        </div>
      ) : isDesktop ? (
        <div className="hair overflow-hidden rounded-xl bg-surface">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                <th className="px-5 py-2.5 font-medium">Name</th>
                <th className="px-3 py-2.5 font-medium">Type</th>
                <th className="px-3 py-2.5 text-right font-medium">Responses</th>
                <th className="px-3 py-2.5 text-right font-medium">Updated</th>
                <th className="px-5 py-2.5" />
              </tr>
            </thead>
            <tbody>
              {shown.map((e) => {
                const Icon = KIND_ICON[e.kind]
                return (
                  <tr key={e.id} className="border-b border-hair/60 last:border-0 hover:bg-ink/[0.015]">
                    <td className="px-5 py-3">
                      <button onClick={() => openInEditor(e.id)} className="block max-w-[46ch] text-left">
                        <span className="block truncate text-[13.5px] font-medium text-ink">{e.title}</span>
                        <span className="tnum block truncate text-[11.5px] text-ink-muted">
                          {e.issuer} · {meta(e)}
                        </span>
                      </button>
                    </td>
                    <td className="px-3 py-3">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <Badge size="xs" variant="light" color={KIND_COLOR[e.kind]} leftSection={<Icon size={10} />}>
                          {KINDS.find((k) => k.v === e.kind)?.label}
                        </Badge>
                        <StatusPill status={e.status} />
                      </div>
                    </td>
                    <td className="tnum px-3 py-3 text-right text-[13px] text-ink-2">
                      {e.kind === 'survey' ? countFor(e.id) : '—'}
                    </td>
                    <td className="tnum px-3 py-3 text-right text-[12px] text-ink-muted">{ago(e.updatedAt)}</td>
                    <td className="px-5 py-3">
                      <div className="flex items-center justify-end gap-1.5">
                        <button
                          onClick={() => previewAsCandidate(e.id)}
                          className="hair grid h-7 w-7 place-items-center rounded-md bg-surface"
                          aria-label={`Preview ${e.title}`}
                        >
                          <IconEye size={14} stroke={1.8} className="text-ink-2" />
                        </button>
                        {actions(e)}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {shown.map((e) => {
            const Icon = KIND_ICON[e.kind]
            return (
              <article key={e.id} className="hair rounded-xl bg-surface p-4">
                <div className="flex items-start justify-between gap-3">
                  <button onClick={() => openInEditor(e.id)} className="min-w-0 flex-1 text-left">
                    <span className="flex flex-wrap items-center gap-1.5">
                      <Badge size="xs" variant="light" color={KIND_COLOR[e.kind]} leftSection={<Icon size={10} />}>
                        {KINDS.find((k) => k.v === e.kind)?.label}
                      </Badge>
                      <StatusPill status={e.status} />
                    </span>
                    <p className="mt-1.5 text-[14.5px] font-semibold leading-snug text-ink">{e.title}</p>
                    <p className="tnum mt-0.5 text-[12px] text-ink-muted">{e.issuer}</p>
                  </button>
                  {actions(e)}
                </div>
                <p className="tnum mt-2 text-[12px] text-ink-2">{meta(e)}</p>
                <div className="mt-3 flex items-center gap-2 border-t border-hair pt-3">
                  {e.status === 'archived' ? (
                    <button
                      onClick={() => setStatus(e.id, 'draft')}
                      className="hair flex flex-1 items-center justify-center gap-1.5 rounded-lg bg-surface py-2 text-[12.5px] font-medium text-ink-2"
                    >
                      <IconArchiveOff size={13} stroke={1.9} /> Restore as draft
                    </button>
                  ) : (
                  <>
                  <button
                    onClick={() => openInEditor(e.id)}
                    className="flex flex-1 items-center justify-center gap-1.5 rounded-lg py-2 text-[12.5px] font-semibold text-on-brand"
                    style={{ background: 'var(--color-s1)' }}
                  >
                    <IconPencil size={13} stroke={2} /> Edit
                  </button>
                  <button
                    onClick={() => previewAsCandidate(e.id)}
                    className="hair flex flex-1 items-center justify-center gap-1.5 rounded-lg bg-surface py-2 text-[12.5px] font-medium text-ink-2"
                  >
                    <IconEye size={13} stroke={1.9} /> Preview
                  </button>
                  </>
                  )}
                </div>
                <p className="tnum mt-2 text-[11px] text-ink-muted">
                  Updated {ago(e.updatedAt)}
                  {e.kind === 'survey' ? ` · ${countFor(e.id)} responses` : ''}
                </p>
              </article>
            )
          })}
        </div>
      )}
    </div>
  )
}
