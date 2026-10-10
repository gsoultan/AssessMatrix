import { Badge } from '@mantine/core'
import { IconWifiOff, IconWifi, IconAntennaBars3, IconEye, IconUserCheck } from '@tabler/icons-react'
import { Panel } from '../components/Panel'
import { StatTile, Meter } from '../components/Viz'
import { liveAttempts, evidenceTimeline } from '../data'

const NET = {
  good: { Icon: IconWifi, color: 'var(--color-ink-muted)', label: 'Good' },
  weak: { Icon: IconAntennaBars3, color: 'var(--color-serious)', label: 'Weak' },
  offline: { Icon: IconWifiOff, color: 'var(--color-ink-2)', label: 'Offline — buffering locally' },
} as const

const TL = {
  ok: 'var(--color-good)',
  warn: 'var(--color-warn)',
  crit: 'var(--color-critical)',
  info: 'var(--color-ink-muted)',
} as const

export function ProctorMonitor() {
  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">Proctor monitor</h1>
          <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
            Live attempts over a Connect server-streaming RPC. Every flag states its reason in plain
            language — a candidate who contests one is owed an answer, and "the model said so" is not one.
          </p>
        </div>
        <span className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1" style={{ background: 'var(--color-wash-good)' }}>
          <span className="pulse-dot h-1.5 w-1.5 rounded-full" style={{ background: 'var(--color-good)' }} />
          <span className="text-[11.5px] font-medium" style={{ color: 'var(--color-good-text)' }}>Streaming · 6 active</span>
        </span>
      </header>

      <div className="mb-5 grid grid-cols-2 gap-3 xl:grid-cols-4">
        <StatTile label="Active attempts" value="6" spark={[2, 3, 3, 4, 4, 5, 5, 6, 6, 6, 6, 6]} />
        <StatTile label="Flags raised" value="7" delta={+16.7} goodWhenDown spark={[2, 3, 2, 4, 3, 5, 4, 6, 5, 7, 6, 7]} period="vs last hour" />
        <StatTile label="Awaiting human review" value="2" spark={[0, 0, 1, 1, 1, 2, 1, 2, 2, 2, 2, 2]} />
        <StatTile label="Identity match rate" value="100%" delta={0} spark={[98, 99, 99, 100, 99, 100, 100, 100, 100, 100, 100, 100]} />
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <Panel title="Live attempts" hint="Ranked by risk. Risk is a sum of explainable rules, not an opaque score." pad={false}>
          <ul>
            {[...liveAttempts]
              .sort((a, b) => b.risk - a.risk)
              .map((a) => {
                const net = NET[a.net as keyof typeof NET]
                return (
                  <li key={a.id} className="border-b border-hair/60 px-4 py-3.5 last:border-0 sm:px-5">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="truncate text-[13.5px] font-medium text-ink">{a.name}</p>
                        <p className="tnum truncate text-[11.5px] text-ink-muted">
                          {a.id} · {a.exam} · {a.min} min elapsed
                        </p>
                      </div>
                      <span className="inline-flex shrink-0 items-center gap-1.5">
                        <net.Icon size={14} stroke={1.8} color={net.color} />
                        <span className="text-[11px] text-ink-muted">{net.label}</span>
                      </span>
                    </div>

                    <div className="mt-2.5 max-w-[320px]">
                      <Meter value={a.risk} sev={a.sev} label="Integrity risk" showWord />
                    </div>

                    {a.flags.length > 0 ? (
                      <div className="mt-2.5 flex flex-wrap gap-1.5">
                        {a.flags.map((f) => (
                          <Badge key={f} size="xs" variant="default" radius="sm">
                            {f}
                          </Badge>
                        ))}
                      </div>
                    ) : (
                      <p className="mt-2.5 text-[11.5px] text-ink-muted">No signals</p>
                    )}
                  </li>
                )
              })}
          </ul>
        </Panel>

        <div className="flex flex-col gap-4">
          <Panel title="Evidence timeline — AT-4821" hint="The attempt event log, rendered. This is also the legal record.">
            <ol className="relative flex flex-col gap-0 border-l border-hair pl-4">
              {evidenceTimeline.map((e, i) => (
                <li key={i} className="relative pb-3.5 last:pb-0">
                  <span
                    className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full"
                    style={{ background: TL[e.kind as keyof typeof TL], boxShadow: '0 0 0 2px var(--color-surface)' }}
                  />
                  <p className="tnum text-[11px] text-ink-muted">{e.t}</p>
                  <p className="mt-0.5 text-[12.5px] leading-snug text-ink-2">{e.label}</p>
                </li>
              ))}
            </ol>
          </Panel>

          <Panel title="Privacy posture" hint="What we are actually collecting right now.">
            <ul className="flex flex-col gap-2.5 text-[12.5px] text-ink-2">
              <li className="flex items-start gap-2">
                <IconEye size={15} stroke={1.8} color="var(--color-ink-muted)" className="mt-px shrink-0" />
                <span>Vision runs <strong className="font-medium text-ink">on device</strong>. Events are uploaded; video is not.</span>
              </li>
              <li className="flex items-start gap-2">
                <IconUserCheck size={15} stroke={1.8} color="var(--color-ink-muted)" className="mt-px shrink-0" />
                <span>Tier 3 keyframes retained <strong className="font-medium text-ink">90 days</strong>, then deleted with an audit record.</span>
              </li>
            </ul>
            <div className="mt-3 rounded-lg px-3 py-2.5" style={{ background: 'var(--color-wash)' }}>
              <p className="text-[11.5px] leading-snug text-ink-2">
                Region <strong className="font-medium text-ink">id-jkt-1</strong> · biometric processing covered
                by DPIA-2026-04 · UU PDP consent v3 recorded per candidate.
              </p>
            </div>
          </Panel>
        </div>
      </div>
    </div>
  )
}
