import type { ReactNode } from 'react'

export function Panel({
  title,
  hint,
  actions,
  children,
  pad = true,
  className = '',
}: {
  title?: ReactNode
  hint?: ReactNode
  actions?: ReactNode
  children: ReactNode
  pad?: boolean
  className?: string
}) {
  return (
    <section className={`hair rounded-xl bg-surface ${className}`}>
      {(title || actions) && (
        <header className="flex items-start justify-between gap-3 border-b border-hair px-4 py-3 sm:px-5">
          <div className="min-w-0">
            {title && <h2 className="text-[13px] font-semibold tracking-tight text-ink">{title}</h2>}
            {hint && <p className="mt-0.5 text-[12px] leading-snug text-ink-muted">{hint}</p>}
          </div>
          {actions && <div className="shrink-0">{actions}</div>}
        </header>
      )}
      <div className={pad ? 'p-4 sm:p-5' : ''}>{children}</div>
    </section>
  )
}

export function Label({ children }: { children: ReactNode }) {
  return (
    <span className="text-[11px] font-medium uppercase tracking-wider text-ink-muted">{children}</span>
  )
}
