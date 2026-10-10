import { useState } from 'react'
import { Badge, SegmentedControl, Tooltip } from '@mantine/core'
import {
  IconCircleCheck,
  IconAlertTriangle,
  IconCircleX,
  IconCopy,
  IconPlus,
  IconX,
  IconShieldLock,
} from '@tabler/icons-react'
import { Panel } from '../components/Panel'
import { doors, preflight } from '../data'

const SNIPPET = `<script src="https://cdn.assessmatrix.id/embed/v3.js" defer></script>

<assessmatrix-runner
  token="{{ attemptToken }}"      <!-- minted by YOUR backend, 15 min TTL -->
  theme="host"                    <!-- inherits your CSS custom properties -->
  locale="id-ID"
></assessmatrix-runner>`

const FLAG = {
  pass: { Icon: IconCircleCheck, color: 'var(--color-good)', bg: 'var(--color-wash-good)', word: 'Pass' },
  warn: { Icon: IconAlertTriangle, color: 'var(--color-serious)', bg: 'var(--color-wash-serious)', word: 'Warning' },
  fail: { Icon: IconCircleX, color: 'var(--color-critical)', bg: 'var(--color-wash-critical)', word: 'Fail' },
} as const

function Preview({ device }: { device: string }) {
  const narrow = device === 'mobile'
  return (
    <div className={`mx-auto w-full ${narrow ? 'max-w-[320px]' : ''}`}>
      <div className="hair overflow-hidden rounded-xl bg-surface">
        {/* host browser chrome */}
        <div className="flex items-center gap-2 border-b border-hair px-3 py-2" style={{ background: 'var(--color-sunken)' }}>
          <div className="flex gap-1">
            {['var(--color-hair)', 'var(--color-hair)', 'var(--color-hair)'].map((c, i) => (
              <span key={i} className="h-2 w-2 rounded-full" style={{ background: c }} />
            ))}
          </div>
          <div className="tnum flex-1 truncate rounded px-2 py-0.5 text-[10.5px] text-ink-muted" style={{ background: 'var(--color-surface)' }}>
            app.your-ats.com/candidates/4821
          </div>
        </div>

        {/* host app content */}
        <div className="bg-surface p-3.5">
          <div className="flex items-center gap-2.5">
            <div className="grid h-8 w-8 place-items-center rounded-full text-[12px] font-semibold" style={{ background: 'var(--color-hair)', color: 'var(--color-ink-2)' }}>
              BS
            </div>
            <div className="min-w-0">
              <p className="truncate text-[12.5px] font-semibold text-ink">Budi Santoso</p>
              <p className="truncate text-[11px] text-ink-muted">Backend Engineer · Tahap 3 dari 5</p>
            </div>
          </div>

          {/* the embed boundary, deliberately visible in the configurator */}
          <div className="relative mt-3 rounded-lg p-3" style={{ border: '1.5px dashed var(--color-track-2)', background: 'var(--color-wash)' }}>
            <span
              className="absolute -top-2 left-2.5 rounded px-1.5 text-[9.5px] font-semibold tracking-wide"
              style={{ background: 'var(--color-s1)', color: 'var(--color-on-brand)' }}
            >
              ASSESSMATRIX IFRAME
            </span>
            <p className="mt-1 text-[12.5px] font-semibold text-ink">Sertifikasi Backend Engineer — L3</p>
            <p className="mt-0.5 text-[11px] text-ink-muted">45 menit · 20 soal · Tier 3 Certified</p>
            <div className="mt-2.5 flex items-center gap-2">
              <button className="rounded-lg px-3 py-1.5 text-[11.5px] font-semibold text-white" style={{ background: 'var(--color-s1)' }}>
                Mulai asesmen
              </button>
              <span className="text-[10.5px] text-ink-muted">Token kedaluwarsa 14:32</span>
            </div>
          </div>
          <p className="mt-2.5 text-[10.5px] leading-snug text-ink-muted">
            Host page cannot read item content, script the runtime, or touch the attempt log.
          </p>
        </div>
      </div>
    </div>
  )
}

export function EmbedStudio() {
  const [mode, setMode] = useState('embed')
  const [device, setDevice] = useState('desktop')
  const [origins, setOrigins] = useState(['https://app.your-ats.com', 'https://*.your-ats.com'])

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Embed studio</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Configure the embed, see it in context, and find out what will break{' '}
          <em className="not-italic font-medium text-ink-2">before</em> you ship it.
        </p>
      </header>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,340px)_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <Panel title="Door" hint="Changing this rewrites the snippet and re-runs preflight.">
            <div className="flex flex-col gap-2">
              {doors.map((d) => {
                const on = d.key === mode
                return (
                  <button
                    key={d.key}
                    onClick={() => setMode(d.key)}
                    className={`flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-left ${
                      on ? 'border-transparent' : 'border-hair hover:bg-ink/[0.02]'
                    }`}
                    style={on ? { background: 'var(--color-wash)', boxShadow: '0 0 0 1.5px var(--color-s1)' } : undefined}
                  >
                    <span
                      className="mt-0.5 h-3.5 w-3.5 shrink-0 rounded-full"
                      style={{ boxShadow: on ? 'inset 0 0 0 4px var(--color-s1)' : 'inset 0 0 0 1.5px var(--color-rule)' }}
                    />
                    <span className="min-w-0">
                      <span className="block text-[13px] font-medium text-ink">
                        {d.n}. {d.name}
                      </span>
                      <span className="mt-0.5 block text-[11.5px] leading-snug text-ink-muted">{d.desc}</span>
                    </span>
                  </button>
                )
              })}
            </div>
          </Panel>

          <Panel title="Allowed origins" hint="Enforced as CSP frame-ancestors. Nothing else may frame the runner.">
            <div className="flex flex-col gap-1.5">
              {origins.map((o) => (
                <div key={o} className="flex items-center gap-2 rounded-lg px-2.5 py-1.5" style={{ background: 'var(--color-sunken)' }}>
                  <span className="tnum min-w-0 flex-1 truncate text-[12px] text-ink-2">{o}</span>
                  <button onClick={() => setOrigins(origins.filter((x) => x !== o))} aria-label={`Remove ${o}`}>
                    <IconX size={13} stroke={2} color="var(--color-ink-muted)" />
                  </button>
                </div>
              ))}
              <button className="mt-1 inline-flex items-center gap-1 text-[12.5px] font-medium" style={{ color: 'var(--color-s1)' }}>
                <IconPlus size={14} stroke={2.2} /> Add origin
              </button>
            </div>
          </Panel>

          <Panel title="Theme bridge" hint="Host tokens cross the postMessage bridge; Mantine and Tailwind both respond.">
            <div className="flex flex-wrap gap-2">
              {[
                ['--brand', 'var(--color-s1)'],
                ['--surface', 'var(--color-surface)'],
                ['--ink', '#0b0b0b'],
                ['--radius', '12px'],
              ].map(([k, v]) => (
                <span key={k} className="hair inline-flex items-center gap-1.5 rounded-md bg-surface px-2 py-1">
                  {v.startsWith('#') && <span className="h-3 w-3 rounded-sm" style={{ background: v, boxShadow: '0 0 0 1px var(--color-hair)' }} />}
                  <span className="tnum text-[11px] text-ink-2">{k}</span>
                  <span className="tnum text-[11px] text-ink-muted">{v}</span>
                </span>
              ))}
            </div>
          </Panel>
        </div>

        <div className="flex flex-col gap-4">
          <Panel
            title="Live preview"
            hint="Your application, with the runner embedded exactly as your candidates will see it."
            actions={
              <SegmentedControl
                size="xs"
                value={device}
                onChange={setDevice}
                data={[
                  { label: 'Mobile', value: 'mobile' },
                  { label: 'Desktop', value: 'desktop' },
                ]}
              />
            }
          >
            <Preview device={device} />
          </Panel>

          {/* ── the screen's reason to exist ── */}
          <Panel
            title="Preflight"
            hint="Re-runs on every change. Two of these fail silently in production on every competing platform."
            actions={
              <Tooltip label="1 failing · 1 warning" withArrow>
                <div>
                  <Badge size="sm" variant="light" color="red">1 blocking</Badge>
                </div>
              </Tooltip>
            }
          >
            <ul className="flex flex-col gap-2">
              {preflight.map((p) => {
                const f = FLAG[p.state]
                return (
                  <li key={p.title} className="flex items-start gap-2.5 rounded-lg p-2.5" style={{ background: f.bg }}>
                    <f.Icon size={16} stroke={1.9} color={f.color} className="mt-px shrink-0" />
                    <div className="min-w-0 flex-1">
                      <p className="text-[12.5px] font-medium text-ink">
                        <span className="sr-only">{f.word}: </span>
                        {p.title}
                      </p>
                      <p className="mt-0.5 text-[12px] leading-snug text-ink-2">{p.detail}</p>
                    </div>
                    {p.fix && (
                      <button className="hair shrink-0 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium text-ink-2">
                        {p.fix}
                      </button>
                    )}
                  </li>
                )
              })}
            </ul>
          </Panel>

          <Panel
            title="Snippet"
            hint="The loader is 5.8 KB gzipped and immutably cached."
            actions={
              <button className="hair inline-flex items-center gap-1.5 rounded-md bg-surface px-2.5 py-1 text-[11.5px] font-medium text-ink-2">
                <IconCopy size={13} stroke={1.9} /> Copy
              </button>
            }
            pad={false}
          >
            <pre className="scroll-thin overflow-x-auto px-4 py-3.5 text-[11.5px] leading-relaxed text-ink-2 sm:px-5">
              <code>{SNIPPET}</code>
            </pre>
            <div className="flex items-start gap-2 border-t border-hair px-4 py-2.5 sm:px-5">
              <IconShieldLock size={14} stroke={1.9} color="var(--color-ink-muted)" className="mt-px shrink-0" />
              <p className="text-[11.5px] leading-snug text-ink-muted">
                Your API key never reaches the browser. The attempt token is scoped to one attempt,
                audience-bound to your origin, and expires in 15 minutes.
              </p>
            </div>
          </Panel>
        </div>
      </div>
    </div>
  )
}
