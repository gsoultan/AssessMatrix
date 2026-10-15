import { useEffect, useMemo, useState } from 'react'
import { NativeSelect, TextInput } from '@mantine/core'
import { IconPlus, IconSearch, IconUserPlus } from '@tabler/icons-react'
import { useApp } from '../state'
import { Panel } from '../components/Panel'
import { NewTenant } from '../components/TenantSwitcher'
import { type Grant, type StaffAccess, type Tenant, tenancy } from '../backend/tenancy'
import { explain } from '../backend/repo'

const GRANTS: { value: Grant; label: string }[] = [
  { value: 'none', label: 'No access' },
  { value: 'member', label: 'Member' },
  { value: 'admin', label: 'Administrator' },
]

/**
 * Who may work in which tenant. One row per person, one column per tenant you
 * administer: a member sees only the tenants ticked for them, and moving
 * between those is a switch, never a view of two at once.
 */
export function Access({ isDesktop }: { isDesktop: boolean }) {
  const me = useApp((s) => s.staff)
  const boot = useApp((s) => s.boot)
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [staff, setStaff] = useState<StaffAccess[] | null>(null)
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState('')
  const [q, setQ] = useState('')
  const [creating, setCreating] = useState(false)

  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [tenantId, setTenantId] = useState('')
  const [role, setRole] = useState<Grant>('member')

  const load = async () => {
    try {
      const r = await tenancy.access()
      setTenants(r.tenants)
      setStaff(r.staff)
      setTenantId((t) => t || r.tenants.find((x) => x.current)?.id || r.tenants[0]?.id || '')
    } catch (err) {
      setNote(explain(err))
      setStaff([])
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const change = async (who: string, tenant: Tenant, grant: Grant, newName = '') => {
    setBusy(`${who}:${tenant.id}`)
    setNote('')
    try {
      await tenancy.setAccess(who, tenant.id, grant, newName)
      const what = grant === 'none' ? 'no longer has access to' : `is now ${grant === 'admin' ? 'an administrator' : 'a member'} of`
      setNote(`${who} ${what} ${tenant.name}.`)
      // Your own role here may have changed what this screen may show.
      if (who.toLowerCase() === me?.email.toLowerCase() && tenant.current) await boot()
      else await load()
    } catch (err) {
      setNote(explain(err))
    } finally {
      setBusy('')
    }
  }

  const shown = useMemo(() => {
    const t = q.trim().toLowerCase()
    return (staff ?? []).filter((s) => !t || s.email.includes(t) || s.name.toLowerCase().includes(t))
  }, [staff, q])

  const add = async () => {
    const e = email.trim().toLowerCase()
    if (!e.includes('@')) {
      setNote('Enter the email address they sign in with.')
      return
    }
    const tenant = tenants.find((t) => t.id === tenantId)
    if (!tenant) return
    await change(e, tenant, role, name.trim())
    setEmail('')
    setName('')
  }

  const cell = (s: StaffAccess, t: Tenant) => (
    <NativeSelect
      size="xs"
      aria-label={`${s.email} in ${t.name}`}
      data={GRANTS}
      value={s.grants[t.id] ?? 'none'}
      disabled={busy !== ''}
      onChange={(ev) => void change(s.email, t, ev.currentTarget.value as Grant)}
    />
  )

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">Access</h1>
          <p className="mt-0.5 max-w-[70ch] text-[12.5px] leading-snug text-ink-muted">
            Who can work in each tenant you administer. Members build, invite and score in the tenants given to
            them and nowhere else; administrators also manage access and share instruments between tenants.
          </p>
        </div>
        <button
          onClick={() => setCreating(true)}
          className="hair inline-flex items-center gap-1.5 rounded-lg bg-surface px-3 py-2 text-[12.5px] font-medium text-ink-2"
        >
          <IconPlus size={14} stroke={1.9} /> New tenant
        </button>
      </header>
      <NewTenant opened={creating} onClose={() => setCreating(false)} onCreated={() => void load()} />

      <Panel title="Give someone access" hint="They sign in with this address; a code is sent to it each time." className="mb-4">
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(ev) => {
            ev.preventDefault()
            void add()
          }}
        >
          <TextInput size="xs" label="Email" className="w-full sm:w-[240px]" value={email} onChange={(e) => setEmail(e.currentTarget.value)} />
          <TextInput size="xs" label="Name" className="w-full sm:w-[180px]" value={name} onChange={(e) => setName(e.currentTarget.value)} />
          <NativeSelect
            size="xs"
            label="Tenant"
            className="w-full sm:w-[180px]"
            data={tenants.map((t) => ({ value: t.id, label: t.name }))}
            value={tenantId}
            onChange={(e) => setTenantId(e.currentTarget.value)}
          />
          <NativeSelect
            size="xs"
            label="Role"
            className="w-full sm:w-[150px]"
            data={GRANTS.filter((g) => g.value !== 'none')}
            value={role}
            onChange={(e) => setRole(e.currentTarget.value as Grant)}
          />
          <button
            type="submit"
            disabled={busy !== ''}
            className="inline-flex items-center gap-1.5 rounded-lg px-3 py-[7px] text-[12.5px] font-semibold text-on-brand disabled:opacity-60"
            style={{ background: 'var(--color-s1)' }}
          >
            <IconUserPlus size={14} stroke={2} /> Give access
          </button>
        </form>
      </Panel>

      {note && (
        <p role="status" className="mb-3 text-[12.5px] text-ink-2">
          {note}
        </p>
      )}

      <div className="mb-3 flex items-center">
        <TextInput
          size="xs"
          className="w-full sm:w-[240px]"
          placeholder="Search by name or email"
          leftSection={<IconSearch size={14} stroke={1.8} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
      </div>

      {staff === null ? (
        <p role="status" className="text-[12.5px] text-ink-muted">
          Loading…
        </p>
      ) : shown.length === 0 ? (
        <p className="text-[12.5px] text-ink-muted">No one matches.</p>
      ) : isDesktop ? (
        <div className="hair overflow-x-auto rounded-xl bg-surface">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                <th className="px-5 py-2.5 font-medium">Person</th>
                {tenants.map((t) => (
                  <th key={t.id} className="px-3 py-2.5 font-medium">
                    {t.name}
                    {t.current && <span className="ml-1 normal-case tracking-normal">(here)</span>}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {shown.map((s) => (
                <tr key={s.email} className="border-b border-hair/60 last:border-0">
                  <td className="px-5 py-2.5">
                    <span className="block text-[13px] font-medium text-ink">{s.name || s.email}</span>
                    {s.name && <span className="block text-[11.5px] text-ink-muted">{s.email}</span>}
                  </td>
                  {tenants.map((t) => (
                    <td key={t.id} className="px-3 py-2.5">
                      {cell(s, t)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {shown.map((s) => (
            <article key={s.email} className="hair rounded-xl bg-surface p-4">
              <p className="text-[13px] font-medium text-ink">{s.name || s.email}</p>
              {s.name && <p className="text-[11.5px] text-ink-muted">{s.email}</p>}
              <div className="mt-3 flex flex-col gap-2">
                {tenants.map((t) => (
                  <div key={t.id} className="flex items-center justify-between gap-3">
                    <span className="min-w-0 truncate text-[12.5px] text-ink-2">{t.name}</span>
                    <div className="w-[150px] shrink-0">{cell(s, t)}</div>
                  </div>
                ))}
              </div>
            </article>
          ))}
        </div>
      )}
    </div>
  )
}
