import { useEffect, useState } from 'react'
import type React from 'react'
import { IconArrowRight, IconCircleCheck, IconAlertTriangle, IconDatabase } from '@tabler/icons-react'
import { ConnectError, Code } from '@connectrpc/connect'
import { setup, type SetupStatus } from '../backend/setup'
import { useApp } from '../state'

const trouble = (err: unknown) => {
  if (!(err instanceof ConnectError)) return 'Could not reach the server. Check your connection and try again.'
  switch (err.code) {
    case Code.PermissionDenied:
      return 'That setup code is wrong. It is in the server log, on the line that says first-run setup is open.'
    case Code.InvalidArgument:
      return err.rawMessage.charAt(0).toUpperCase() + err.rawMessage.slice(1) + '.'
    case Code.FailedPrecondition:
      return 'This server is already set up. Sign in instead.'
    case Code.ResourceExhausted:
      return 'Too many tries from here. Wait a minute, then try again.'
    default:
      return 'Could not reach the server. Check your connection and try again.'
  }
}

/**
 * First run, at /setup: checks the database, then creates the organisation
 * and its first administrator, who is signed in straight away. Once that is
 * done the server refuses a second setup, and this page says so.
 */
export function Setup() {
  const signedIn = useApp((s) => s.signedIn)
  const [status, setStatus] = useState<SetupStatus | 'error' | undefined>()
  const [form, setForm] = useState({ code: '', organization: '', name: '', email: '', region: '' })
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  useEffect(() => {
    setup.status().then(
      (s) => {
        setStatus(s)
        setForm((f) => ({ ...f, region: f.region || s.region }))
      },
      () => setStatus('error'),
    )
  }, [])

  const toStudio = () => window.history.replaceState(null, '', '/')

  const submit = async () => {
    if (!form.code.trim()) return setNote('Enter the setup code from the server log.')
    if (!form.organization.trim()) return setNote('Name your organisation.')
    if (!form.email.includes('@')) return setNote('Enter your work email. You will sign in with it.')
    setBusy(true)
    setNote('')
    try {
      const staff = await setup.complete(form)
      toStudio()
      await signedIn(staff)
    } catch (err) {
      setNote(trouble(err))
      if (err instanceof ConnectError && err.code === Code.FailedPrecondition)
        setStatus((s) => (s && s !== 'error' ? { ...s, needed: false } : s))
    } finally {
      setBusy(false)
    }
  }

  const field = 'hair mt-1.5 h-11 w-full rounded-lg bg-surface px-3 text-[14px] text-ink'
  const label = 'mt-4 block text-[12.5px] font-medium text-ink'
  const primary =
    'mt-5 flex h-11 w-full items-center justify-center gap-2 rounded-lg text-[14px] font-semibold text-on-brand disabled:opacity-50'
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.currentTarget.value
    setForm((f) => ({ ...f, [k]: v }))
  }

  const behind = status && status !== 'error' && status.schemaVersion < status.schemaLatest

  return (
    <div className="grid min-h-full place-items-center bg-plane p-4">
      <div className="hair w-full max-w-[440px] rounded-2xl bg-surface p-6">
        <div className="mb-5 flex items-center gap-2.5">
          <div className="grid h-8 w-8 place-items-center rounded-md" style={{ background: 'var(--color-s1)' }}>
            <span className="text-[14px] font-bold text-white">A</span>
          </div>
          <p className="text-[15px] font-semibold tracking-tight">AssessMatrix</p>
        </div>

        {status === undefined && (
          <p role="status" className="text-[13px] text-ink-muted">
            Checking the server…
          </p>
        )}

        {status === 'error' && (
          <p role="alert" className="text-[13px]" style={{ color: 'var(--color-critical)' }}>
            Could not reach the server. Check that it is running, then reload this page.
          </p>
        )}

        {status && status !== 'error' && !status.needed && (
          <div>
            <h1 className="text-[17px] font-semibold tracking-tight">Already set up</h1>
            <p className="mt-1 text-[12.5px] leading-snug text-ink-muted">
              This server has its organisation and administrator. To add people, ask an administrator.
            </p>
            <a href="/" className={primary} style={{ background: 'var(--color-s1)' }}>
              Go to sign in <IconArrowRight size={16} stroke={2.2} />
            </a>
          </div>
        )}

        {status && status !== 'error' && status.needed && (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void submit()
            }}
          >
            <h1 className="text-[17px] font-semibold tracking-tight">Set up AssessMatrix</h1>
            <p className="mt-1 text-[12.5px] leading-snug text-ink-muted">
              Create your organisation and its first administrator. This page works once; after that, people sign in.
            </p>

            <div
              className="mt-4 flex items-start gap-2.5 rounded-lg p-3"
              style={{ background: behind ? 'var(--color-wash-warn)' : 'var(--color-wash-good)' }}
            >
              <IconDatabase size={18} stroke={1.7} className="mt-0.5 shrink-0" />
              <div className="text-[12.5px] leading-snug">
                <p className="flex items-center gap-1.5 font-medium text-ink">
                  Database
                  {behind ? (
                    <IconAlertTriangle size={15} stroke={2} style={{ color: 'var(--color-warn)' }} />
                  ) : (
                    <IconCircleCheck size={15} stroke={2} style={{ color: 'var(--color-good)' }} />
                  )}
                </p>
                <p className="text-ink-2">
                  {behind
                    ? `Schema ${status.schemaVersion || 'missing'}, this version needs ${status.schemaLatest}. Run core migrate, then reload.`
                    : `Connected, schema up to date (${status.schemaLatest}).`}
                </p>
              </div>
            </div>

            <label htmlFor="setup-code" className={label}>
              Setup code
            </label>
            <input
              id="setup-code"
              autoComplete="off"
              autoCapitalize="characters"
              spellCheck={false}
              autoFocus
              placeholder="XXXX-XXXX-XXXX"
              value={form.code}
              onChange={set('code')}
              aria-describedby="setup-code-help"
              className={`${field} tnum tracking-[0.12em]`}
            />
            <p id="setup-code-help" className="mt-1 text-[12px] leading-snug text-ink-muted">
              The server prints it to its log when it starts. You can also run <code>core setup-code</code> on the
              server.
            </p>

            <label htmlFor="setup-org" className={label}>
              Organisation name
            </label>
            <input id="setup-org" autoComplete="organization" value={form.organization} onChange={set('organization')} className={field} />

            <label htmlFor="setup-name" className={label}>
              Your name
            </label>
            <input id="setup-name" autoComplete="name" value={form.name} onChange={set('name')} className={field} />

            <label htmlFor="setup-email" className={label}>
              Your work email
            </label>
            <input
              id="setup-email"
              type="email"
              autoComplete="email"
              value={form.email}
              onChange={set('email')}
              aria-describedby="setup-email-help"
              className={field}
            />
            <p id="setup-email-help" className="mt-1 text-[12px] leading-snug text-ink-muted">
              You will be the administrator. Later you sign in with this address and a code sent to it.
            </p>

            <label htmlFor="setup-region" className={label}>
              Data region
            </label>
            <input
              id="setup-region"
              value={form.region}
              onChange={set('region')}
              aria-describedby="setup-region-help"
              className={field}
            />
            <p id="setup-region-help" className="mt-1 text-[12px] leading-snug text-ink-muted">
              Where candidates' data and images are kept. This server stores in <strong>{status.region}</strong>.
            </p>

            <button type="submit" disabled={busy || !!behind} className={primary} style={{ background: 'var(--color-s1)' }}>
              Create organisation and sign in <IconArrowRight size={16} stroke={2.2} />
            </button>
          </form>
        )}

        <p role="alert" className="mt-3 min-h-[18px] text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
          {note}
        </p>
      </div>
    </div>
  )
}
