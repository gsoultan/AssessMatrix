import { Badge } from '@mantine/core'
import { IconShieldCheck, IconWallet, IconExternalLink, IconSchool, IconBriefcase, IconCertificate } from '@tabler/icons-react'
import { Panel } from '../components/Panel'
import { SkillBar } from '../components/Viz'
import { skills, evidenceSources } from '../data'

const CTX = {
  Certify: { Icon: IconCertificate, color: 'var(--color-s1)' },
  Campus: { Icon: IconSchool, color: 'var(--color-s3)' },
  Hire: { Icon: IconBriefcase, color: 'var(--color-s2)' },
} as const

export function EvidenceProfile() {
  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Evidence profile</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          The Matrix: skill × evidence × context. One person's evidence accumulates across school,
          certification and hiring — and the credential lives in a wallet they own, not in our database.
        </p>
      </header>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <Panel
            title="Skill evidence"
            hint="A level with no confidence interval is a lie. Thin evidence is shown as thin evidence."
          >
            <div className="divide-y divide-hair/60">
              {skills.map((s) => (
                <SkillBar key={s.name} {...s} />
              ))}
            </div>
            <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-hair pt-3 text-[11.5px] text-ink-muted">
              <span className="inline-flex items-center gap-1.5">
                <span className="h-2.5 w-1 rounded-sm" style={{ background: 'var(--color-s1)' }} />
                Point estimate
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="h-2 w-5 rounded-full opacity-45" style={{ background: 'var(--color-s1)' }} />
                95% confidence band
              </span>
              <span className="tnum">n = observations</span>
            </div>
          </Panel>

          <Panel title="Where the evidence came from" hint="Cross-context accumulation is the part no single-segment platform can build.">
            <ul className="flex flex-col gap-2.5">
              {evidenceSources.map((e) => {
                const c = CTX[e.ctx as keyof typeof CTX]
                return (
                  <li key={e.label} className="flex items-start gap-3 rounded-lg border border-hair px-3 py-2.5">
                    <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg" style={{ background: `color-mix(in srgb, ${c.color} 12%, transparent)` }}>
                      <c.Icon size={15} stroke={1.8} color={c.color} />
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-[13px] font-medium text-ink">{e.label}</p>
                      <p className="truncate text-[11.5px] text-ink-muted">{e.weight}</p>
                    </div>
                    <span className="tnum shrink-0 text-[11.5px] text-ink-muted">{e.when}</span>
                  </li>
                )
              })}
            </ul>
          </Panel>
        </div>

        <div className="flex flex-col gap-4">
          <div className="hair overflow-hidden rounded-xl">
            <div className="p-4 text-white" style={{ background: 'linear-gradient(135deg, var(--color-credential-from) 0%, var(--color-credential-to) 100%)' }}>
              <div className="flex items-start justify-between gap-3">
                <div>
                  <p className="text-[10.5px] font-medium uppercase tracking-wider opacity-80">Verifiable credential</p>
                  <p className="mt-1.5 text-[15px] font-semibold leading-tight">Backend Engineer — Level 3</p>
                  <p className="mt-0.5 text-[12px] opacity-85">LSP Teknologi Informasi</p>
                </div>
                <IconShieldCheck size={26} stroke={1.6} className="shrink-0 opacity-90" />
              </div>
              <dl className="mt-4 grid grid-cols-2 gap-y-2 text-[11.5px]">
                <dt className="opacity-75">Issued</dt>
                <dd className="tnum text-right">21 Sep 2026</dd>
                <dt className="opacity-75">Expires</dt>
                <dd className="tnum text-right">21 Sep 2029</dd>
                <dt className="opacity-75">Standard</dt>
                <dd className="text-right">Open Badges 3.0</dd>
              </dl>
            </div>
            <div className="flex flex-col gap-2 bg-surface p-3.5">
              <button className="flex items-center justify-center gap-1.5 rounded-lg py-2 text-[12.5px] font-semibold text-white" style={{ background: 'var(--color-s1)' }}>
                <IconWallet size={15} stroke={2} /> Add to wallet
              </button>
              <button className="hair flex items-center justify-center gap-1.5 rounded-lg bg-surface py-2 text-[12.5px] font-medium text-ink-2">
                <IconExternalLink size={14} stroke={1.9} /> Public verification link
              </button>
              <p className="mt-0.5 text-[11px] leading-snug text-ink-muted">
                Verification is free and needs no account — ubiquity is what makes an issued credential
                worth holding.
              </p>
            </div>
          </div>

          <Panel title="Portability" hint="Ours to deliver, not to hold hostage.">
            <div className="flex flex-wrap gap-1.5">
              {['W3C VC', 'Open Badges 3.0', 'CLR 2.0', 'QTI 3.0 export', 'xAPI'].map((s) => (
                <Badge key={s} size="xs" variant="default" radius="sm">{s}</Badge>
              ))}
            </div>
            <p className="mt-3 text-[12px] leading-snug text-ink-2">
              Revocation is published to the status list; a revoked credential fails verification
              everywhere within minutes, without us contacting a single relying party.
            </p>
          </Panel>
        </div>
      </div>
    </div>
  )
}
