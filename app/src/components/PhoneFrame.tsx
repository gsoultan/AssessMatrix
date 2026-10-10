import type { ReactNode } from 'react'

/** Desktop-only device frame so the mobile-first screens can be judged at their real width. */
export function PhoneFrame({ children }: { children: ReactNode }) {
  return (
    <div className="mx-auto w-full max-w-[390px]">
      <div className="hair overflow-hidden rounded-[2rem] bg-surface p-2 shadow-sm">
        <div className="relative overflow-hidden rounded-[1.5rem] bg-plane">
          <div className="pointer-events-none absolute left-1/2 top-2 z-20 h-5 w-24 -translate-x-1/2 rounded-full bg-ink/85" />
          <div className="flex h-[780px] flex-col">{children}</div>
        </div>
      </div>
    </div>
  )
}
