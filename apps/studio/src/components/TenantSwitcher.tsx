import { useState } from 'react'
import { Menu, Modal, TextInput } from '@mantine/core'
import { IconCheck, IconPlus, IconSelector } from '@tabler/icons-react'
import { useApp } from '../state'
import { type Tenant, tenancy } from '../backend/tenancy'
import { explain } from '../backend/repo'

/**
 * Which tenant this session works in, and the way to another. The list is
 * read when the menu opens, so access granted a minute ago shows up without a
 * reload. A super administrator can start a new tenant from here too.
 */
export function TenantSwitcher() {
  const staff = useApp((s) => s.staff)
  const switchTenant = useApp((s) => s.switchTenant)
  const [tenants, setTenants] = useState<Tenant[] | null>(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  if (!staff) return null

  const load = async () => {
    setError('')
    try {
      setTenants(await tenancy.mine())
    } catch (err) {
      setError(explain(err))
    }
  }

  return (
    <>
      <Menu position="bottom-start" width={240} onOpen={() => void load()}>
        <Menu.Target>
          <button
            className="-mx-1.5 flex min-w-0 flex-1 items-center gap-1 rounded-md px-1.5 py-1 text-left hover:bg-ink/[0.04]"
            aria-label={`Tenant: ${staff.tenantName}. Switch tenant`}
          >
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-semibold leading-none tracking-tight">{staff.tenantName}</span>
              <span className="mt-1 block truncate text-[11px] leading-none text-ink-muted">
                {staff.superAdmin ? 'Super administrator' : staff.role === 'admin' ? 'Administrator' : 'Member'}
              </span>
            </span>
            <IconSelector size={14} stroke={1.8} className="shrink-0 text-ink-muted" />
          </button>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>Your tenants</Menu.Label>
          {error && <p className="px-3 py-1.5 text-[12px] text-ink-muted">{error}</p>}
          {!error && !tenants && <p className="px-3 py-1.5 text-[12px] text-ink-muted">Loading…</p>}
          {tenants?.map((t) => (
            <Menu.Item
              key={t.id}
              onClick={() => !t.current && void switchTenant(t.id)}
              rightSection={t.current ? <IconCheck size={14} stroke={2} /> : null}
              aria-current={t.current ? 'true' : undefined}
            >
              <span className="block truncate text-[12.5px]">{t.name}</span>
              <span className="block text-[11px] text-ink-muted">{t.role === 'admin' ? 'Administrator' : 'Member'}</span>
            </Menu.Item>
          ))}
          {staff.superAdmin && (
            <>
              <Menu.Divider />
              <Menu.Item leftSection={<IconPlus size={14} stroke={1.9} />} onClick={() => setCreating(true)}>
                New tenant
              </Menu.Item>
            </>
          )}
        </Menu.Dropdown>
      </Menu>
      <NewTenant opened={creating} onClose={() => setCreating(false)} />
    </>
  )
}

/**
 * A new tenant and its first administrator, for a super administrator. With no
 * administrator named the creator is it, and is switched there.
 */
export function NewTenant({ opened, onClose, onCreated }: { opened: boolean; onClose: () => void; onCreated?: (t: Tenant) => void }) {
  const switchTenant = useApp((s) => s.switchTenant)
  const [name, setName] = useState('')
  const [region, setRegion] = useState('')
  const [adminEmail, setAdminEmail] = useState('')
  const [adminName, setAdminName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    if (!name.trim()) {
      setError('Give the tenant a name.')
      return
    }
    if (adminEmail.trim() && !adminEmail.includes('@')) {
      setError('Enter the email address the administrator signs in with, or leave it empty.')
      return
    }
    setBusy(true)
    setError('')
    try {
      const t = await tenancy.create(name.trim(), region.trim().toLowerCase(), adminEmail.trim().toLowerCase(), adminName.trim())
      setName('')
      setRegion('')
      setAdminEmail('')
      setAdminName('')
      onClose()
      if (onCreated) onCreated(t)
      else if (t.role !== 'none') await switchTenant(t.id)
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal opened={opened} onClose={onClose} title="New tenant" centered>
      <p className="mb-3 text-[12.5px] leading-snug text-ink-muted">
        A tenant keeps its own instruments, people, invitations and results, apart from every other. Its
        administrator adds the users who work there. Leave the administrator empty to be it yourself.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
        className="flex flex-col gap-3"
      >
        <TextInput label="Name" value={name} onChange={(e) => setName(e.currentTarget.value)} data-autofocus maxLength={200} />
        <TextInput
          label="Data region"
          description="Where its personal data and images are kept. Leave empty to use this tenant's region."
          placeholder="id"
          value={region}
          onChange={(e) => setRegion(e.currentTarget.value)}
          maxLength={20}
        />
        <TextInput
          label="Administrator's email"
          description="They are emailed that they can sign in. Leave empty to administer it yourself."
          type="email"
          value={adminEmail}
          onChange={(e) => setAdminEmail(e.currentTarget.value)}
          maxLength={320}
        />
        {adminEmail.trim() && (
          <TextInput label="Administrator's name" value={adminName} onChange={(e) => setAdminName(e.currentTarget.value)} maxLength={200} />
        )}
        {error && (
          <p role="alert" className="text-[12.5px] text-ink">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="px-3 py-2 text-[12.5px] font-medium text-ink-2">
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy}
            className="rounded-lg px-3 py-2 text-[12.5px] font-semibold text-on-brand disabled:opacity-60"
            style={{ background: 'var(--color-s1)' }}
          >
            Create tenant
          </button>
        </div>
      </form>
    </Modal>
  )
}
