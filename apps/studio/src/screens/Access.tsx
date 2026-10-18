import { useEffect, useMemo, useState } from 'react'
import { NativeSelect, SegmentedControl, Select, TextInput } from '@mantine/core'
import { IconPlus, IconSearch, IconUserPlus, IconX } from '@tabler/icons-react'
import { useApp } from '../state'
import { Panel } from '../components/Panel'
import { NewTenant } from '../components/TenantSwitcher'
import { type Grant, type StaffAccess, type Tenant, tenancy } from '../backend/tenancy'
import type { StaffRole } from '../backend/staff'
import { explain } from '../backend/repo'

const ROLES: { value: StaffRole; label: string }[] = [
  { value: 'member', label: 'Member' },
  { value: 'admin', label: 'Administrator' },
]

/** Rows drawn at once; search narrows the rest. Keeps a large organisation responsive. */
const SHOWN = 100

const roleName = (r: StaffRole) => (r === 'admin' ? 'Administrator' : 'Member')
const matches = (q: string, ...fields: string[]) => {
  const t = q.trim().toLowerCase()
  return !t || fields.some((f) => f.toLowerCase().includes(t))
}

/**
 * Which studio users (staff, never test takers) may work in which tenant. One
 * tenant, or one user, at a time: with dozens of tenants a user-by-tenant grid
 * stops fitting, so each view lists
 * only what has access and adds the rest from a search.
 */
export function Access({ isDesktop }: { isDesktop: boolean }) {
  const me = useApp((s) => s.staff)
  const boot = useApp((s) => s.boot)
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [staff, setStaff] = useState<StaffAccess[] | null>(null)
  const [view, setView] = useState<'tenant' | 'person'>('tenant')
  const [tenantId, setTenantId] = useState('')
  const [email, setEmail] = useState('')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [creating, setCreating] = useState(false)

  const load = async () => {
    try {
      const r = await tenancy.access()
      setTenants(r.tenants)
      setStaff(r.staff)
      setTenantId((t) => (r.tenants.some((x) => x.id === t) ? t : r.tenants.find((x) => x.current)?.id || r.tenants[0]?.id || ''))
    } catch (err) {
      setNote(explain(err))
      setStaff([])
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const change = async (who: string, tenant: Tenant, grant: Grant, newName = ''): Promise<boolean> => {
    setBusy(true)
    setNote('')
    try {
      await tenancy.setAccess(who, tenant.id, grant, newName)
      const what = grant === 'none' ? 'no longer has access to' : `is now ${grant === 'admin' ? 'an administrator' : 'a member'} of`
      setNote(`${who} ${what} ${tenant.name}.`)
      // Your own role here may have changed what this screen may show.
      if (who.toLowerCase() === me?.email.toLowerCase() && tenant.current) await boot()
      else await load()
      return true
    } catch (err) {
      setNote(explain(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  const counts = useMemo(() => {
    const n: Record<string, number> = {}
    for (const s of staff ?? []) for (const id of Object.keys(s.grants)) n[id] = (n[id] ?? 0) + 1
    return n
  }, [staff])

  const ctx = { tenants, staff: staff ?? [], counts, busy, change, isDesktop }

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">Access</h1>
          <p className="mt-0.5 max-w-[70ch] text-[12.5px] leading-snug text-ink-muted">
            Which studio users can work in each tenant you administer. Members build, invite and score in the
            tenants given to them and nowhere else; administrators also manage access and share instruments between
            tenants. Test takers are not users: they are in People and only ever see the tests they are invited to.
          </p>
        </div>
        <button
          onClick={() => setCreating(true)}
          className="hair inline-flex items-center gap-1.5 rounded-lg bg-surface px-3 py-2 text-[12.5px] font-medium text-ink-2"
        >
          <IconPlus size={14} stroke={1.9} /> New tenant
        </button>
      </header>
      <NewTenant
        opened={creating}
        onClose={() => setCreating(false)}
        onCreated={(t) => {
          setTenantId(t.id)
          setView('tenant')
          void load()
        }}
      />

      <SegmentedControl
        size="xs"
        className="mb-4"
        value={view}
        onChange={(v) => setView(v as 'tenant' | 'person')}
        data={[
          { value: 'tenant', label: `By tenant (${tenants.length})` },
          { value: 'person', label: `By user (${staff?.length ?? 0})` },
        ]}
      />

      {note && (
        <p role="status" className="mb-3 text-[12.5px] text-ink-2">
          {note}
        </p>
      )}

      {staff === null ? (
        <p role="status" className="text-[12.5px] text-ink-muted">
          Loading…
        </p>
      ) : view === 'tenant' ? (
        <ByTenant {...ctx} selected={tenantId} onSelect={setTenantId} onPerson={(e) => (setEmail(e), setView('person'))} />
      ) : (
        <ByPerson {...ctx} selected={email} onSelect={setEmail} />
      )}
    </div>
  )
}

type Ctx = {
  tenants: Tenant[]
  staff: StaffAccess[]
  counts: Record<string, number>
  busy: boolean
  change: (who: string, tenant: Tenant, grant: Grant, name?: string) => Promise<boolean>
  isDesktop: boolean
}

/** A searchable list on the left, the chosen item on the right; a picker above it on a phone. */
function TwoPane({
  isDesktop,
  list,
  picker,
  children,
}: {
  isDesktop: boolean
  list: React.ReactNode
  picker: React.ReactNode
  children: React.ReactNode
}) {
  if (!isDesktop)
    return (
      <div className="flex flex-col gap-3">
        {picker}
        {children}
      </div>
    )
  return (
    <div className="grid grid-cols-[280px_minmax(0,1fr)] items-start gap-4">
      <div className="hair sticky top-4 flex max-h-[calc(100vh-2rem)] flex-col rounded-xl bg-surface">{list}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function ListSearch({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <div className="border-b border-hair p-2">
      <TextInput
        size="xs"
        aria-label={placeholder}
        placeholder={placeholder}
        leftSection={<IconSearch size={14} stroke={1.8} />}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
      />
    </div>
  )
}

function ListItem({ active, onClick, title, sub }: { active: boolean; onClick: () => void; title: string; sub: string }) {
  return (
    <li>
      <button
        onClick={onClick}
        aria-current={active ? 'true' : undefined}
        className={`block w-full px-3 py-1.5 text-left ${active ? 'bg-ink/[0.06]' : 'hover:bg-ink/[0.03]'}`}
      >
        <span className="block truncate text-[12.5px] font-medium text-ink">{title}</span>
        <span className="block truncate text-[11px] text-ink-muted">{sub}</span>
      </button>
    </li>
  )
}

function More({ total }: { total: number }) {
  if (total <= SHOWN) return null
  return <p className="px-3 py-2 text-[11.5px] text-ink-muted">{total - SHOWN} more. Search to narrow the list.</p>
}

const tenantSub = (t: Tenant, n: number) => `${n} ${n === 1 ? 'user' : 'users'}${t.current ? ' · you are here' : ''}`

function ByTenant({
  tenants,
  staff,
  counts,
  busy,
  change,
  isDesktop,
  selected,
  onSelect,
  onPerson,
}: Ctx & { selected: string; onSelect: (id: string) => void; onPerson: (email: string) => void }) {
  const [q, setQ] = useState('')
  const [pq, setPq] = useState('')
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState<StaffRole>('member')
  const [err, setErr] = useState('')
  const tenant = tenants.find((t) => t.id === selected)
  // Where you are now first; the rest as the core sorts them, by name.
  const found = tenants.filter((t) => matches(q, t.name)).sort((a, b) => Number(b.current) - Number(a.current))
  const people = tenant ? staff.filter((s) => s.grants[tenant.id] && matches(pq, s.email, s.name)) : []
  const admins = tenant ? staff.filter((s) => s.grants[tenant.id] === 'admin').length : 0

  useEffect(() => setPq(''), [selected])

  const add = async () => {
    const e = email.trim().toLowerCase()
    if (!e.includes('@')) return setErr('Enter the email address they sign in with.')
    setErr('')
    if (tenant && (await change(e, tenant, role, name.trim()))) {
      setEmail('')
      setName('')
    }
  }

  const list = (
    <>
      <ListSearch value={q} onChange={setQ} placeholder="Find a tenant" />
      <ul className="min-h-0 overflow-y-auto py-1">
        {found.slice(0, SHOWN).map((t) => (
          <ListItem key={t.id} active={t.id === selected} onClick={() => onSelect(t.id)} title={t.name} sub={tenantSub(t, counts[t.id] ?? 0)} />
        ))}
        {found.length === 0 && <p className="px-3 py-2 text-[12px] text-ink-muted">No tenant matches.</p>}
      </ul>
      <More total={found.length} />
    </>
  )
  const picker = (
    <Select
      size="sm"
      label="Tenant"
      searchable
      allowDeselect={false}
      nothingFoundMessage="No tenant matches"
      data={tenants.map((t) => ({ value: t.id, label: t.name }))}
      value={selected || null}
      onChange={(v) => v && onSelect(v)}
    />
  )

  return (
    <TwoPane isDesktop={isDesktop} list={list} picker={picker}>
      {!tenant ? (
        <p className="text-[12.5px] text-ink-muted">Choose a tenant.</p>
      ) : (
        <div className="flex flex-col gap-4">
          <Panel
            pad={false}
            title={tenant.name}
            hint={`${counts[tenant.id] ?? 0} with access · ${admins} ${admins === 1 ? 'administrator' : 'administrators'}`}
            actions={
              <TextInput
                size="xs"
                aria-label={`Search users in ${tenant.name}`}
                placeholder="Search users"
                leftSection={<IconSearch size={14} stroke={1.8} />}
                value={pq}
                onChange={(e) => setPq(e.currentTarget.value)}
                className="w-[180px] sm:w-[220px]"
              />
            }
          >
            <form
              className="flex flex-wrap items-end gap-2 border-b border-hair px-4 py-3 sm:px-5"
              aria-label={`Give a user access to ${tenant.name}`}
              onSubmit={(ev) => {
                ev.preventDefault()
                void add()
              }}
            >
              <TextInput size="xs" label="Email" type="email" className="w-full sm:w-auto sm:min-w-[200px] sm:flex-1" value={email} onChange={(e) => setEmail(e.currentTarget.value)} />
              <TextInput size="xs" label="Name" className="w-full sm:w-[160px]" value={name} onChange={(e) => setName(e.currentTarget.value)} />
              <NativeSelect size="xs" label="Role" className="w-full sm:w-[140px]" data={ROLES} value={role} onChange={(e) => setRole(e.currentTarget.value as StaffRole)} />
              <SubmitButton busy={busy} icon={<IconUserPlus size={14} stroke={2} />} label="Give access" />
              {err && <p role="alert" className="w-full text-[12px] text-ink">{err}</p>}
            </form>
            {people.length === 0 ? (
              <p className="px-4 py-3 text-[12.5px] text-ink-muted sm:px-5">{pq ? 'No user matches.' : 'No user has access yet.'}</p>
            ) : (
              <ul>
                {people.slice(0, SHOWN).map((s) => (
                  <AccessRow
                    key={s.email}
                    title={s.name || s.email}
                    sub={s.name ? s.email : ''}
                    onTitle={() => onPerson(s.email)}
                    role={s.grants[tenant.id]}
                    label={`${s.email} in ${tenant.name}`}
                    busy={busy}
                    onRole={(r) => void change(s.email, tenant, r)}
                    onRemove={() => void change(s.email, tenant, 'none')}
                  />
                ))}
              </ul>
            )}
            <More total={people.length} />
          </Panel>
        </div>
      )}
    </TwoPane>
  )
}

function ByPerson({ tenants, staff, busy, change, isDesktop, selected, onSelect }: Ctx & { selected: string; onSelect: (email: string) => void }) {
  const [q, setQ] = useState('')
  const [addTo, setAddTo] = useState<string | null>(null)
  const [role, setRole] = useState<StaffRole>('member')
  const [hq, setHq] = useState('')
  const found = staff.filter((s) => matches(q, s.email, s.name))
  const person = staff.find((s) => s.email === selected) ?? found[0]
  const all = person ? tenants.filter((t) => person.grants[t.id]) : []
  const held = all.filter((t) => matches(hq, t.name))
  const rest = person ? tenants.filter((t) => !person.grants[t.id]) : []

  useEffect(() => (setAddTo(null), setHq('')), [person?.email])

  const sub = (s: StaffAccess) => {
    const n = Object.keys(s.grants).length
    return `${s.name ? s.email + ' · ' : ''}${n} ${n === 1 ? 'tenant' : 'tenants'}`
  }
  const list = (
    <>
      <ListSearch value={q} onChange={setQ} placeholder="Find a user" />
      <ul className="min-h-0 overflow-y-auto py-1">
        {found.slice(0, SHOWN).map((s) => (
          <ListItem key={s.email} active={s.email === person?.email} onClick={() => onSelect(s.email)} title={s.name || s.email} sub={sub(s)} />
        ))}
        {found.length === 0 && <p className="px-3 py-2 text-[12px] text-ink-muted">No user matches.</p>}
      </ul>
      <More total={found.length} />
    </>
  )
  const picker = (
    <Select
      size="sm"
      label="User"
      searchable
      allowDeselect={false}
      nothingFoundMessage="No user matches"
      data={staff.map((s) => ({ value: s.email, label: s.name ? `${s.name} (${s.email})` : s.email }))}
      value={person?.email ?? null}
      onChange={(v) => v && onSelect(v)}
    />
  )

  return (
    <TwoPane isDesktop={isDesktop} list={list} picker={picker}>
      {!person ? (
        <p className="text-[12.5px] text-ink-muted">No user has access to the tenants you administer yet.</p>
      ) : (
        <Panel
          pad={false}
          title={person.name || person.email}
          hint={`${person.name ? person.email + ' · ' : ''}${all.length} of ${tenants.length} tenants`}
          actions={
            all.length > 8 && (
              <TextInput
                size="xs"
                aria-label={`Search the tenants of ${person.email}`}
                placeholder="Search their tenants"
                leftSection={<IconSearch size={14} stroke={1.8} />}
                value={hq}
                onChange={(e) => setHq(e.currentTarget.value)}
                className="w-[180px] sm:w-[220px]"
              />
            )
          }
        >
          <form
            className="flex flex-wrap items-end gap-2 border-b border-hair px-4 py-3 sm:px-5"
            onSubmit={async (ev) => {
              ev.preventDefault()
              const t = tenants.find((x) => x.id === addTo)
              if (t && (await change(person.email, t, role))) setAddTo(null)
            }}
          >
            <Select
              size="xs"
              label="Add to a tenant"
              placeholder={rest.length ? 'Find a tenant' : 'Already in every tenant you administer'}
              disabled={rest.length === 0}
              searchable
              nothingFoundMessage="No tenant matches"
              className="w-full sm:w-[260px]"
              data={rest.map((t) => ({ value: t.id, label: t.name }))}
              value={addTo}
              onChange={setAddTo}
            />
            <NativeSelect size="xs" label="Role" className="w-full sm:w-[150px]" data={ROLES} value={role} onChange={(e) => setRole(e.currentTarget.value as StaffRole)} />
            <SubmitButton busy={busy || !addTo} icon={<IconPlus size={14} stroke={2} />} label="Add" />
          </form>
          {held.length === 0 && <p className="px-4 py-3 text-[12.5px] text-ink-muted sm:px-5">No tenant matches.</p>}
          <ul>
            {held.map((t) => (
              <AccessRow
                key={t.id}
                title={t.name}
                sub={t.current ? 'You are here' : ''}
                role={person.grants[t.id]}
                label={`${person.email} in ${t.name}`}
                busy={busy}
                onRole={(r) => void change(person.email, t, r)}
                onRemove={() => void change(person.email, t, 'none')}
              />
            ))}
          </ul>
        </Panel>
      )}
    </TwoPane>
  )
}

function AccessRow({
  title,
  sub,
  onTitle,
  role,
  label,
  busy,
  onRole,
  onRemove,
}: {
  title: string
  sub: string
  onTitle?: () => void
  role: StaffRole
  label: string
  busy: boolean
  onRole: (r: StaffRole) => void
  onRemove: () => void
}) {
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-hair/60 px-4 py-2.5 last:border-0 sm:px-5">
      <div className="min-w-0 flex-1">
        {onTitle ? (
          <button onClick={onTitle} className="block max-w-full truncate text-left text-[13px] font-medium text-ink hover:underline">
            {title}
          </button>
        ) : (
          <span className="block truncate text-[13px] font-medium text-ink">{title}</span>
        )}
        {sub && <span className="block truncate text-[11.5px] text-ink-muted">{sub}</span>}
      </div>
      <NativeSelect
        size="xs"
        aria-label={`Role of ${label}`}
        className="w-[150px]"
        data={ROLES}
        value={role}
        disabled={busy}
        onChange={(e) => onRole(e.currentTarget.value as StaffRole)}
      />
      <button
        onClick={onRemove}
        disabled={busy}
        aria-label={`Remove ${label}`}
        title={`Remove ${roleName(role).toLowerCase()} access`}
        className="rounded-md p-1.5 text-ink-muted hover:bg-ink/[0.05] hover:text-ink disabled:opacity-50"
      >
        <IconX size={15} stroke={1.9} />
      </button>
    </li>
  )
}

function SubmitButton({ busy, icon, label }: { busy: boolean; icon: React.ReactNode; label: string }) {
  return (
    <button
      type="submit"
      disabled={busy}
      className="inline-flex items-center gap-1.5 rounded-lg px-3 py-[7px] text-[12.5px] font-semibold text-on-brand disabled:opacity-60"
      style={{ background: 'var(--color-s1)' }}
    >
      {icon} {label}
    </button>
  )
}
