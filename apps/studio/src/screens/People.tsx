import { useMemo, useState } from 'react'
import { TextInput, Textarea, Collapse, Badge, Tooltip } from '@mantine/core'
import {
  IconSearch,
  IconUserPlus,
  IconUpload,
  IconTrash,
  IconUsers,
  IconCheck,
  IconSend,
} from '@tabler/icons-react'
import { useApp } from '../state'
import { Panel } from '../components/Panel'
import { RosterImport } from '../components/RosterImport'

export function People({ isDesktop }: { isDesktop: boolean }) {
  const people = useApp((s) => s.people)
  const assignments = useApp((s) => s.assignments)
  const exams = useApp((s) => s.exams)
  const addPerson = useApp((s) => s.addPerson)
  const importRoster = useApp((s) => s.importRoster)
  const addPeople = useApp((s) => s.addPeople)
  const removePerson = useApp((s) => s.removePerson)
  const setScreen = useApp((s) => s.setScreen)

  const [q, setQ] = useState('')
  const [group, setGroup] = useState('all')
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [newGroup, setNewGroup] = useState('')
  const [importOpen, setImportOpen] = useState(false)
  const [paste, setPaste] = useState('')
  const [note, setNote] = useState('')

  const groups = useMemo(
    () => [...new Set(people.map((p) => p.group).filter(Boolean))].sort(),
    [people],
  )

  const shown = useMemo(
    () =>
      people
        .filter((p) => group === 'all' || p.group === group)
        .filter((p) => {
          const t = q.trim().toLowerCase()
          return !t || p.name.toLowerCase().includes(t) || p.email.toLowerCase().includes(t)
        }),
    [people, q, group],
  )

  const countFor = (personId: string) => assignments.filter((a) => a.personId === personId).length
  const submittedFor = (personId: string) =>
    assignments.filter((a) => a.personId === personId && a.status === 'submitted').length

  const submit = () => {
    const e = email.trim()
    if (!e.includes('@')) {
      setNote('An email address is required — it is how an invitation reaches someone.')
      return
    }
    if (people.some((p) => p.email.toLowerCase() === e.toLowerCase())) {
      setNote('Someone with that email is already on the roster.')
      return
    }
    addPerson(name.trim() || e.split('@')[0]!, e, newGroup.trim())
    setName('')
    setEmail('')
    setNote('')
  }

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">People</h1>
          <p className="mt-0.5 max-w-[70ch] text-[12.5px] leading-snug text-ink-muted">
            The roster you assign from. Publishing an instrument does not deliver it — giving a
            named person a way in does, and that is an assignment.
          </p>
        </div>
        <button
          onClick={() => setImportOpen((o) => !o)}
          className="hair inline-flex items-center gap-1.5 rounded-lg bg-surface px-3 py-2 text-[12.5px] font-medium text-ink-2"
        >
          <IconUpload size={14} stroke={1.9} /> Import
        </button>
      </header>

      <Collapse expanded={importOpen} keepMounted={false}>
        <Panel
          title="Import a roster"
          hint="From a file, or pasted. Either way, an email already on the roster is skipped, never duplicated."
          className="mb-4"
        >
          <RosterImport
            roster={people}
            action="Add"
            onConfirm={async (drafts) => {
              const n = await addPeople(drafts)
              setNote(`Added ${n} from the file.`)
            }}
          />
          <p className="mb-1.5 mt-4 border-t border-hair pt-3 text-[12px] font-medium text-ink-2">
            Or paste, one per line — a bare email, “Name &lt;email&gt;”, or “Name, email, group”.
          </p>
          <Textarea
            aria-label="Paste a roster"
            autosize
            minRows={4}
            placeholder={'Budi Santoso, budi@example.id, Backend\nsari@example.id\nAndi Pratama <andi@example.id>'}
            value={paste}
            onChange={(e) => setPaste(e.currentTarget.value)}
          />
          <div className="mt-3 flex items-center gap-2">
            <button
              onClick={async () => {
                const r = await importRoster(paste)
                setNote(
                  r.added === 0 && r.skipped === 0
                    ? 'Nothing recognisable in that paste.'
                    : `Added ${r.added}${r.skipped ? `, skipped ${r.skipped} already on the roster` : ''}.`,
                )
                if (r.added) setPaste('')
              }}
              className="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-[12.5px] font-semibold text-on-brand"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconCheck size={14} stroke={2.2} /> Import roster
            </button>
            <button onClick={() => setImportOpen(false)} className="text-[12.5px] font-medium text-ink-2">
              Cancel
            </button>
          </div>
        </Panel>
      </Collapse>

      <Panel title="Add someone" className="mb-4">
        <div className="flex flex-wrap items-end gap-2">
          <TextInput
            size="xs"
            label="Name"
            placeholder="optional"
            className="min-w-[150px] flex-1"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            size="xs"
            label="Email"
            placeholder="name@example.id"
            className="min-w-[190px] flex-1"
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && submit()}
          />
          <TextInput
            size="xs"
            label="Group"
            placeholder="optional"
            className="min-w-[120px] flex-1"
            value={newGroup}
            onChange={(e) => setNewGroup(e.currentTarget.value)}
          />
          <button
            onClick={submit}
            className="inline-flex h-[30px] shrink-0 items-center gap-1.5 rounded-lg px-3 text-[12.5px] font-semibold text-on-brand"
            style={{ background: 'var(--color-s1)' }}
          >
            <IconUserPlus size={14} stroke={2} /> Add
          </button>
        </div>
        {note && <p className="mt-2 text-[12px] text-ink-2">{note}</p>}
      </Panel>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        {(['all', ...groups] as string[]).map((g) => (
          <button
            key={g}
            onClick={() => setGroup(g)}
            className="hair rounded-lg bg-surface px-3 py-1.5 text-[12.5px] font-medium"
            style={
              group === g
                ? { background: 'var(--color-wash)', color: 'var(--color-brand-text)' }
                : { color: 'var(--color-ink-2)' }
            }
          >
            {g === 'all' ? `All ${people.length}` : g}
          </button>
        ))}
        <TextInput
          size="xs"
          className="ml-auto w-full sm:w-[220px]"
          placeholder="Search name or email"
          leftSection={<IconSearch size={14} stroke={1.8} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
      </div>

      {shown.length === 0 ? (
        <div className="hair rounded-xl bg-surface py-14 text-center">
          <IconUsers size={28} stroke={1.4} className="mx-auto text-ink-muted" />
          <p className="mt-3 text-[13.5px] font-medium text-ink">
            {people.length === 0 ? 'No one on the roster yet' : 'No matches'}
          </p>
          <p className="mt-1 text-[12.5px] text-ink-muted">
            {people.length === 0 ? 'Add someone above, or paste a list with Import.' : 'Try a different group or search.'}
          </p>
        </div>
      ) : isDesktop ? (
        <div className="hair overflow-hidden rounded-xl bg-surface">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                <th className="px-5 py-2.5 font-medium">Name</th>
                <th className="px-3 py-2.5 font-medium">Group</th>
                <th className="px-3 py-2.5 text-right font-medium">Assigned</th>
                <th className="px-3 py-2.5 text-right font-medium">Submitted</th>
                <th className="px-5 py-2.5" />
              </tr>
            </thead>
            <tbody>
              {shown.map((p) => (
                <tr key={p.id} className="border-b border-hair/60 last:border-0">
                  <td className="px-5 py-3">
                    <p className="text-[13.5px] font-medium text-ink">{p.name}</p>
                    <p className="tnum text-[11.5px] text-ink-muted">{p.email}</p>
                  </td>
                  <td className="px-3 py-3">
                    {p.group ? <Badge size="xs" variant="default">{p.group}</Badge> : <span className="text-[12px] text-ink-muted">—</span>}
                  </td>
                  <td className="tnum px-3 py-3 text-right text-[13px] text-ink-2">{countFor(p.id)}</td>
                  <td className="tnum px-3 py-3 text-right text-[13px] text-ink-2">{submittedFor(p.id)}</td>
                  <td className="px-5 py-3 text-right">
                    <Tooltip label="Remove from roster and cancel their invitations" withArrow>
                      <button onClick={() => removePerson(p.id)} aria-label={`Remove ${p.name}`}>
                        <IconTrash size={14} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
                      </button>
                    </Tooltip>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {shown.map((p) => (
            <div key={p.id} className="hair rounded-xl bg-surface p-4">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-[14px] font-semibold text-ink">{p.name}</p>
                  <p className="tnum truncate text-[12px] text-ink-muted">{p.email}</p>
                </div>
                <button onClick={() => removePerson(p.id)} aria-label={`Remove ${p.name}`} className="shrink-0">
                  <IconTrash size={14} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
                </button>
              </div>
              <p className="tnum mt-2 text-[12px] text-ink-2">
                {p.group ? `${p.group} · ` : ''}
                {countFor(p.id)} assigned · {submittedFor(p.id)} submitted
              </p>
            </div>
          ))}
        </div>
      )}

      {exams.length > 0 && people.length > 0 && (
        <button
          onClick={() => setScreen('assignments')}
          className="mt-4 inline-flex items-center gap-1.5 text-[12.5px] font-medium"
          style={{ color: 'var(--color-s1)' }}
        >
          <IconSend size={14} stroke={2} /> Assign people to an instrument
        </button>
      )}
    </div>
  )
}
