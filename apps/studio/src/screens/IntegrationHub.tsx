import { Badge } from '@mantine/core'
import { IconArrowRight, IconBook2, IconPlugConnected } from '@tabler/icons-react'
import { Panel } from '../components/Panel'
import { StatTile, StatusDot } from '../components/Viz'
import { doors, connectedApps, tiles } from '../data'

const stateColor = { live: 'green', beta: 'blue', enterprise: 'gray' } as const

export function IntegrationHub() {
  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Integration hub</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Integration depth is a spectrum, not a decision. Start at Door 1 in ten minutes and go
          deeper without re-platforming — every door speaks the same schema.
        </p>
      </header>

      <div className="mb-5 grid grid-cols-2 gap-3 xl:grid-cols-4">
        {tiles.map((t) => (
          <StatTile key={t.label} {...t} />
        ))}
      </div>

      <div className="mb-5 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {doors.map((d) => (
          <article key={d.key} className="hair group flex flex-col rounded-xl bg-surface p-4 transition-shadow hover:shadow-sm">
            <div className="flex items-start justify-between gap-2">
              <span
                className="tnum grid h-7 w-7 place-items-center rounded-lg text-[13px] font-bold"
                style={{ background: 'var(--color-wash)', color: 'var(--color-brand-text)' }}
              >
                {d.n}
              </span>
              <Badge size="xs" variant="light" color={stateColor[d.state]}>
                {d.state}
              </Badge>
            </div>
            <h3 className="mt-3 text-[14px] font-semibold tracking-tight text-ink">{d.name}</h3>
            <p className="mt-1 flex-1 text-[12.5px] leading-snug text-ink-2">{d.desc}</p>
            <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1.5 border-t border-hair pt-3 text-[11.5px]">
              <dt className="text-ink-muted">Effort</dt>
              <dd className="text-right font-medium text-ink">{d.effort}</dd>
              <dt className="text-ink-muted">Offline</dt>
              <dd className="text-right font-medium text-ink">{d.offline}</dd>
            </dl>
            <button className="mt-3 inline-flex items-center gap-1 text-[12.5px] font-medium" style={{ color: 'var(--color-s1)' }}>
              Set up <IconArrowRight size={14} stroke={2} />
            </button>
          </article>
        ))}
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Panel
          title="Connected applications"
          hint="Every connection reports its own health. A silent integration is a broken one."
          pad={false}
        >
          <div className="scroll-thin overflow-x-auto">
            <table className="w-full min-w-[560px] text-left">
              <thead>
                <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                  <th className="px-4 py-2 font-medium sm:px-5">Application</th>
                  <th className="px-3 py-2 font-medium">Door</th>
                  <th className="px-3 py-2 text-right font-medium">Events 24h</th>
                  <th className="px-3 py-2 font-medium">Health</th>
                  <th className="px-4 py-2 text-right font-medium sm:px-5">Last</th>
                </tr>
              </thead>
              <tbody>
                {connectedApps.map((a) => (
                  <tr key={a.name} className="border-b border-hair/60 last:border-0">
                    <td className="px-4 py-3 sm:px-5">
                      <p className="text-[13px] font-medium text-ink">{a.name}</p>
                      <p className="text-[11.5px] text-ink-muted">{a.kind}</p>
                    </td>
                    <td className="px-3 py-3">
                      <Badge size="xs" variant="default">{a.door}</Badge>
                    </td>
                    <td className="tnum px-3 py-3 text-right text-[13px] text-ink-2">{a.events.toLocaleString()}</td>
                    <td className="px-3 py-3">
                      <StatusDot
                        sev={a.health}
                        label={a.health === 'ok' ? 'Healthy' : a.health === 'warning' ? 'Degraded' : 'Failing'}
                      />
                    </td>
                    <td className="tnum px-4 py-3 text-right text-[12px] text-ink-muted sm:px-5">{a.last}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>

        <div className="flex flex-col gap-4">
          <Panel title="Standards doors" hint="Orthogonal to the four. Adapters over the same core, never a fork.">
            <ul className="flex flex-col gap-2.5">
              {[
                ['QTI 3.0', 'Item bank in / out', 'live'],
                ['Open Badges 3.0', 'Credential issuance', 'live'],
                ['LTI 1.3', 'Launch from any LMS', 'live'],
                ['OIDC / SAML', 'SSO', 'live'],
                ['SCIM 2.0', 'Roster provisioning', 'beta'],
                ['xAPI / Caliper', 'Analytics egress', 'planned'],
              ].map(([name, what, state]) => (
                <li key={name} className="flex items-center justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate text-[13px] font-medium text-ink">{name}</p>
                    <p className="truncate text-[11.5px] text-ink-muted">{what}</p>
                  </div>
                  <Badge size="xs" variant="light" color={state === 'live' ? 'green' : state === 'beta' ? 'blue' : 'gray'}>
                    {state}
                  </Badge>
                </li>
              ))}
            </ul>
          </Panel>

          <div className="hair rounded-xl p-4" style={{ background: 'var(--color-wash)' }}>
            <p className="flex items-center gap-1.5 text-[13px] font-semibold text-ink">
              <IconPlugConnected size={16} stroke={1.9} color="var(--color-s1)" />
              No lock-in, in writing
            </p>
            <p className="mt-1.5 text-[12.5px] leading-snug text-ink-2">
              Export your entire item bank as QTI 3.0 at any time, including from the free plan.
              Credentials are W3C Verifiable Credentials in a wallet the candidate owns.
            </p>
            <button className="mt-2.5 inline-flex items-center gap-1 text-[12.5px] font-medium" style={{ color: 'var(--color-s1)' }}>
              <IconBook2 size={14} stroke={2} /> Read the data portability policy
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
