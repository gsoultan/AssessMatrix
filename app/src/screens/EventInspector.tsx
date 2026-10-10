import { Badge } from '@mantine/core'
import { IconRefresh, IconPlayerPlay, IconHistory } from '@tabler/icons-react'
import { Panel } from '../components/Panel'
import { StatTile } from '../components/Viz'
import { deliveries } from '../data'

function Code({ code }: { code: number }) {
  const ok = code >= 200 && code < 300
  const warn = code === 429
  const color = ok ? 'var(--color-good)' : warn ? 'var(--color-warn)' : 'var(--color-critical)'
  const bg = ok ? 'var(--color-wash-good)' : warn ? 'var(--color-wash-warn)' : 'var(--color-wash-critical)'
  return (
    <span className="tnum inline-flex items-center gap-1.5 rounded px-1.5 py-0.5 text-[11.5px] font-medium" style={{ background: bg, color }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: color }} />
      {code}
    </span>
  )
}

export function EventInspector() {
  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Event inspector</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Because attempts are event-sourced, replay is free. Resend any event, any attempt, or any
          time window — without opening a support ticket.
        </p>
      </header>

      <div className="mb-5 grid grid-cols-2 gap-3 xl:grid-cols-4">
        <StatTile label="Delivered 24h" value="14,208" delta={+4.1} spark={[52, 55, 51, 58, 61, 57, 63, 66, 62, 70, 73, 78]} />
        <StatTile label="Failed" value="112" delta={-22.4} goodWhenDown spark={[18, 16, 17, 14, 15, 12, 13, 11, 10, 9, 8, 7]} />
        <StatTile label="p95 latency" value="211ms" delta={-6.0} goodWhenDown spark={[31, 29, 30, 27, 28, 26, 25, 24, 23, 22, 22, 21]} />
        <StatTile label="In dead letter" value="3" delta={0} spark={[1, 1, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3]} />
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Panel
          title="Recent deliveries"
          hint="Ordered per attempt, at-least-once, idempotency key on every payload."
          actions={
            <button className="hair inline-flex items-center gap-1.5 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium text-ink-2">
              <IconRefresh size={13} stroke={1.9} /> Live
            </button>
          }
          pad={false}
        >
          <div className="scroll-thin overflow-x-auto">
            <table className="w-full min-w-[640px] text-left">
              <thead>
                <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                  <th className="px-4 py-2 font-medium sm:px-5">Event</th>
                  <th className="px-3 py-2 font-medium">Target</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 text-right font-medium">Latency</th>
                  <th className="px-3 py-2 text-right font-medium">Tries</th>
                  <th className="px-4 py-2 text-right font-medium sm:px-5"></th>
                </tr>
              </thead>
              <tbody>
                {deliveries.map((d) => (
                  <tr key={d.id} className="border-b border-hair/60 last:border-0 hover:bg-ink/[0.015]">
                    <td className="px-4 py-3 sm:px-5">
                      <p className="tnum text-[12.5px] font-medium text-ink">{d.event}</p>
                      <p className="tnum text-[11px] text-ink-muted">{d.id} · {d.at}</p>
                    </td>
                    <td className="px-3 py-3 text-[12.5px] text-ink-2">{d.target}</td>
                    <td className="px-3 py-3"><Code code={d.code} /></td>
                    <td className="tnum px-3 py-3 text-right text-[12.5px] text-ink-2">
                      {d.ms > 1000 ? `${(d.ms / 1000).toFixed(1)}s` : `${d.ms}ms`}
                    </td>
                    <td className="tnum px-3 py-3 text-right text-[12.5px] text-ink-2">{d.tries}</td>
                    <td className="px-4 py-3 text-right sm:px-5">
                      <button className="hair inline-flex items-center gap-1 rounded-md bg-surface px-2 py-1 text-[11px] font-medium text-ink-2">
                        <IconPlayerPlay size={11} stroke={2} /> Replay
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>

        <div className="flex flex-col gap-4">
          <Panel title="Replay a window" hint="Rebuilt from the attempt log, not from a delivery cache.">
            <div className="flex flex-col gap-2.5">
              {[
                ['Attempt', 'AT-4821'],
                ['From', '2026-09-21 14:00'],
                ['To', '2026-09-21 16:00'],
                ['Target', 'Talenta HRIS'],
              ].map(([k, v]) => (
                <div key={k} className="flex items-center justify-between gap-3">
                  <span className="text-[12px] text-ink-muted">{k}</span>
                  <span className="tnum hair rounded-md bg-surface px-2 py-1 text-[12px] text-ink-2">{v}</span>
                </div>
              ))}
            </div>
            <button
              className="mt-3.5 flex w-full items-center justify-center gap-1.5 rounded-lg py-2 text-[13px] font-semibold text-white"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconHistory size={15} stroke={2} /> Replay 41 events
            </button>
            <p className="mt-2 text-[11.5px] leading-snug text-ink-muted">
              Replays carry the original idempotency keys, so a correctly built consumer de-duplicates them.
            </p>
          </Panel>

          <Panel title="Subscribed events" hint="Talenta HRIS">
            <div className="flex flex-wrap gap-1.5">
              {['attempt.scored', 'credential.issued', 'credential.revoked', 'evidence.recorded', 'integrity.flag.raised'].map((e) => (
                <Badge key={e} size="xs" variant="default" radius="sm" className="tnum">
                  {e}
                </Badge>
              ))}
            </div>
          </Panel>
        </div>
      </div>
    </div>
  )
}
