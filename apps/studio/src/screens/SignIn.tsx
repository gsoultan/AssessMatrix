import { useEffect, useState } from 'react'
import { IconArrowRight, IconMailCheck } from '@tabler/icons-react'
import { ConnectError, Code } from '@connectrpc/connect'
import { staffAuth } from '../backend/staff'
import { setup } from '../backend/setup'

const trouble = (err: unknown) =>
  err instanceof ConnectError && err.code === Code.ResourceExhausted
    ? 'Too many tries from here. Wait a few minutes, then try again.'
    : 'Could not reach the server. Check your connection and try again.'
import { useApp } from '../state'

/**
 * Staff sign-in: an email address, then the six-digit code sent to it. The
 * first step answers the same whether or not the address is known, so this
 * page cannot be used to find out who works where.
 */
export function SignIn() {
  const signedIn = useApp((s) => s.signedIn)
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [step, setStep] = useState<'email' | 'code'>('email')
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  // A fresh install has nobody to sign in yet: send the visitor to setup.
  useEffect(() => {
    setup.status().then(
      (s) => s.needed && window.location.replace('/setup'),
      () => {},
    )
  }, [])

  const send = async () => {
    if (!email.includes('@')) return setNote('Enter the email address you were invited with.')
    setBusy(true)
    setNote('')
    try {
      await staffAuth.requestCode(email.trim())
      setStep('code')
    } catch (err) {
      setNote(trouble(err))
    } finally {
      setBusy(false)
    }
  }

  const verify = async () => {
    setBusy(true)
    setNote('')
    try {
      const staff = await staffAuth.verifyCode(email.trim(), code)
      if (staff) return void (await signedIn(staff))
      setCode('')
      setNote('That code is wrong or has expired. Check the latest email, or ask for a new code.')
    } catch (err) {
      setNote(trouble(err))
    } finally {
      setBusy(false)
    }
  }

  const field = 'hair mt-1.5 h-11 w-full rounded-lg bg-surface px-3 text-[14px] text-ink'
  const primary =
    'mt-4 flex h-11 w-full items-center justify-center gap-2 rounded-lg text-[14px] font-semibold text-on-brand disabled:opacity-50'

  return (
    <div className="grid min-h-full place-items-center bg-plane p-4">
      <div className="hair w-full max-w-[380px] rounded-2xl bg-surface p-6">
        <div className="mb-5 flex items-center gap-2.5">
          <div className="grid h-8 w-8 place-items-center rounded-md" style={{ background: 'var(--color-s1)' }}>
            <span className="text-[14px] font-bold text-white">A</span>
          </div>
          <p className="text-[15px] font-semibold tracking-tight">AssessMatrix</p>
        </div>

        {step === 'email' ? (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void send()
            }}
          >
            <h1 className="text-[17px] font-semibold tracking-tight">Sign in</h1>
            <p className="mt-1 text-[12.5px] leading-snug text-ink-muted">We will email you a six-digit code. There is no password.</p>
            <label htmlFor="staff-email" className="mt-4 block text-[12.5px] font-medium text-ink">
              Work email
            </label>
            <input
              id="staff-email"
              type="email"
              autoComplete="email"
              autoFocus
              value={email}
              onChange={(e) => setEmail(e.currentTarget.value)}
              className={field}
            />
            <button type="submit" disabled={busy} className={primary} style={{ background: 'var(--color-s1)' }}>
              Email me a code <IconArrowRight size={16} stroke={2.2} />
            </button>
          </form>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (code.length === 6) void verify()
            }}
          >
            <span className="grid h-10 w-10 place-items-center rounded-lg" style={{ background: 'var(--color-wash)' }}>
              <IconMailCheck size={20} stroke={1.7} style={{ color: 'var(--color-s1)' }} />
            </span>
            <h1 className="mt-3 text-[17px] font-semibold tracking-tight">Check your email</h1>
            <p className="mt-1 text-[12.5px] leading-snug text-ink-muted">
              If <strong className="font-medium text-ink-2">{email}</strong> has an account, a code is on its way. It
              works for 10 minutes.
            </p>
            <label htmlFor="staff-code" className="mt-4 block text-[12.5px] font-medium text-ink">
              Code
            </label>
            <input
              id="staff-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              autoFocus
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.currentTarget.value.replace(/\D/g, '').slice(0, 6))}
              className={`${field} tnum text-[20px] font-semibold tracking-[0.35em]`}
            />
            <button type="submit" disabled={busy || code.length !== 6} className={primary} style={{ background: 'var(--color-s1)' }}>
              Sign in
            </button>
            <button
              type="button"
              onClick={() => {
                setStep('email')
                setCode('')
                setNote('')
              }}
              className="mt-2 h-9 w-full text-[12.5px] font-medium text-ink-2"
            >
              Use a different address, or send a new code
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
