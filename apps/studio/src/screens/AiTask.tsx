import { useState } from 'react'
import { Badge, Tooltip } from '@mantine/core'
import {
  IconSparkles,
  IconPlayerPlay,
  IconFile,
  IconFolder,
  IconCheck,
  IconAlertTriangle,
  IconUser,
  IconLock,
  IconTestPipe,
} from '@tabler/icons-react'
import { Panel } from '../components/Panel'
import { Meter } from '../components/Viz'

const CODE = [
  [1, 'func (s *Server) Resolve(ctx context.Context, key string) (*Entry, error) {', 'plain'],
  [2, '\tif e, ok := s.cache[key]; ok {', 'plain'],
  [3, '\t\treturn e, nil', 'plain'],
  [4, '\t}', 'plain'],
  [5, '', 'plain'],
  [6, '\te, err := s.upstream.Fetch(ctx, key)', 'plain'],
  [7, '\tif err != nil {', 'plain'],
  [8, '\t\treturn nil, err', 'plain'],
  [9, '\t}', 'plain'],
  [10, '', 'plain'],
  [11, '\ts.mu.Lock()', 'added'],
  [12, '\ts.cache[key] = e', 'added'],
  [13, '\ts.mu.Unlock()', 'added'],
  [14, '\treturn e, nil', 'plain'],
  [15, '}', 'plain'],
] as const

const CHAT = [
  {
    who: 'candidate' as const,
    text: 'Resolve is racing under load. Fix it.',
  },
  {
    who: 'ai' as const,
    text: 'Add a sync.Mutex around the cache write. I\'ve applied the change on lines 11–13 — the map is now safe for concurrent use.',
  },
  {
    who: 'candidate' as const,
    text: 'The read on line 2 is still unguarded — that\'s the actual race. And `key` comes from the request path, so this cache grows without bound. Show me a bounded LRU with a read lock.',
    caught: true,
  },
]

const RUBRIC = [
  { name: 'Directs the assistant', v: 82, sev: 'ok' as const, note: 'Specific, scoped prompts' },
  { name: 'Verifies output', v: 74, sev: 'ok' as const, note: 'Ran tests before accepting 3 of 4' },
  { name: 'Recognises wrong answers', v: 91, sev: 'ok' as const, note: 'Caught the injected defect in 38s' },
  { name: 'Integrates & tests', v: 47, sev: 'warning' as const, note: 'No test added for the eviction path' },
]

export function AiTask() {
  const [tab, setTab] = useState<'chat' | 'evidence'>('chat')

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[15px] font-semibold tracking-tight">AI-in-the-loop task</h1>
          <p className="mt-0.5 max-w-[76ch] text-[12.5px] leading-snug text-ink-muted">
            The candidate <em className="not-italic font-medium text-ink-2">gets</em> the assistant.
            We score the collaboration — whether they direct it, verify it, and catch it when it is
            confidently wrong. Detection is an arms race; this is not.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge size="sm" variant="light" color="blue" leftSection={<IconSparkles size={11} />}>
            Assistant enabled
          </Badge>
          <span className="tnum flex items-center gap-1.5 rounded-lg bg-sunken px-2.5 py-1.5 text-[13px] font-semibold text-ink">
            <IconLock size={13} stroke={2} className="text-ink-muted" />
            28:14
          </span>
        </div>
      </header>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,380px)]">
        {/* workspace */}
        <Panel
          title="cache/resolver.go"
          hint="A real multi-file project, not a puzzle box. Tests are runnable."
          actions={
            <button
              className="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11.5px] font-semibold text-on-brand"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconPlayerPlay size={12} stroke={2.2} /> Run tests
            </button>
          }
          pad={false}
        >
          <div className="grid lg:grid-cols-[168px_minmax(0,1fr)]">
            <aside className="border-b border-hair p-3 lg:border-b-0 lg:border-r">
              <p className="mb-2 flex items-center gap-1.5 text-[11.5px] font-medium text-ink-2">
                <IconFolder size={13} stroke={1.8} className="text-ink-muted" /> cache
              </p>
              {['resolver.go', 'resolver_test.go', 'lru.go', 'server.go'].map((f, i) => (
                <p
                  key={f}
                  className={`tnum flex items-center gap-1.5 rounded px-1.5 py-1 text-[11.5px] ${
                    i === 0 ? 'font-medium text-ink' : 'text-ink-muted'
                  }`}
                  style={i === 0 ? { background: 'var(--color-wash)' } : undefined}
                >
                  <IconFile size={12} stroke={1.7} /> {f}
                </p>
              ))}
              <div className="mt-3 rounded-lg p-2" style={{ background: 'var(--color-wash-critical)' }}>
                <p className="flex items-center gap-1.5 text-[11px] font-medium" style={{ color: 'var(--color-critical)' }}>
                  <IconTestPipe size={12} stroke={2} /> 2 failing
                </p>
                <p className="tnum mt-0.5 text-[10.5px] leading-snug text-ink-2">
                  TestConcurrentResolve<br />TestCacheBounded
                </p>
              </div>
            </aside>

            <div className="scroll-thin overflow-x-auto py-2">
              {CODE.map(([n, line, kind]) => (
                <div
                  key={n}
                  className="flex items-start gap-3 px-3 leading-[1.7]"
                  style={kind === 'added' ? { background: 'var(--color-wash-good)' } : undefined}
                >
                  <span className="tnum w-5 shrink-0 select-none text-right text-[11px] text-ink-muted">{n}</span>
                  {kind === 'added' && (
                    <span className="select-none text-[11px]" style={{ color: 'var(--color-good-text)' }}>+</span>
                  )}
                  <code className="tnum whitespace-pre text-[12px] text-ink-2">{line || ' '}</code>
                </div>
              ))}
              <div className="mx-3 mt-2 flex items-start gap-2 rounded-lg p-2.5" style={{ background: 'var(--color-wash-warn)' }}>
                <IconAlertTriangle size={14} stroke={1.9} className="mt-px shrink-0" style={{ color: 'var(--color-serious)' }} />
                <p className="text-[11.5px] leading-snug text-ink-2">
                  Assistant edit applied to lines 11–13. It has <strong className="font-medium text-ink">not</strong> been
                  verified by a test run.
                </p>
              </div>
            </div>
          </div>
        </Panel>

        {/* assistant + scoring */}
        <div className="flex flex-col gap-4">
          <Panel
            title={
              <span className="flex items-center gap-1.5">
                <IconSparkles size={14} stroke={1.9} style={{ color: 'var(--color-s1)' }} />
                Assistant
              </span>
            }
            hint="Every turn is recorded. The transcript is part of the evidence."
            pad={false}
          >
            <div className="flex border-b border-hair">
              {(['chat', 'evidence'] as const).map((t) => (
                <button
                  key={t}
                  onClick={() => setTab(t)}
                  className={`flex-1 px-3 py-2 text-[12px] font-medium capitalize ${
                    tab === t ? 'text-ink' : 'text-ink-muted'
                  }`}
                  style={tab === t ? { boxShadow: 'inset 0 -2px 0 var(--color-s1)' } : undefined}
                >
                  {t === 'chat' ? 'Transcript' : 'Process evidence'}
                </button>
              ))}
            </div>

            {tab === 'chat' ? (
              <div className="flex flex-col gap-3 p-4">
                {CHAT.map((m, i) => (
                  <div key={i} className={`flex gap-2.5 ${m.who === 'candidate' ? '' : ''}`}>
                    <span
                      className="mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full"
                      style={{ background: m.who === 'ai' ? 'var(--color-wash)' : 'var(--color-sunken)' }}
                    >
                      {m.who === 'ai' ? (
                        <IconSparkles size={12} stroke={1.9} style={{ color: 'var(--color-s1)' }} />
                      ) : (
                        <IconUser size={12} stroke={1.9} className="text-ink-muted" />
                      )}
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className="text-[12.5px] leading-snug text-ink-2">{m.text}</p>
                      {m.caught && (
                        <p
                          className="mt-1.5 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] font-medium"
                          style={{ background: 'var(--color-wash-good)', color: 'var(--color-good-text)' }}
                        >
                          <IconCheck size={11} stroke={2.4} /> Caught the injected defect · 38s
                        </p>
                      )}
                    </div>
                  </div>
                ))}
                <div className="mt-1 rounded-lg border border-dashed border-rule p-2.5">
                  <p className="text-[11.5px] leading-snug text-ink-muted">
                    The assistant was instructed to propose a plausible but incomplete fix. Whether the
                    candidate accepts it is the measurement.
                  </p>
                </div>
              </div>
            ) : (
              <div className="flex flex-col gap-2.5 p-4">
                {[
                  ['Assistant turns', '4'],
                  ['Accepted without a test run', '1 of 4'],
                  ['Test runs before accepting', '3'],
                  ['Time to first run', '2m 14s'],
                  ['Revisions after assistant output', '7'],
                  ['Pasted from outside the workspace', '0'],
                ].map(([k, v]) => (
                  <div key={k} className="flex items-baseline justify-between gap-3">
                    <span className="text-[12px] text-ink-2">{k}</span>
                    <span className="tnum text-[12.5px] font-medium text-ink">{v}</span>
                  </div>
                ))}
                <p className="mt-1 border-t border-hair pt-2.5 text-[11.5px] leading-snug text-ink-muted">
                  This is the trajectory, not the artifact. A pasted answer has no trajectory — which is
                  why it needs no camera to detect.
                </p>
              </div>
            )}
          </Panel>

          <Panel title="Collaboration rubric" hint="Scored from the transcript and the edit history, not the final diff.">
            <div className="flex flex-col gap-3.5">
              {RUBRIC.map((r) => (
                <div key={r.name}>
                  <Meter value={r.v} sev={r.sev} label={r.name} />
                  <p className="mt-1 text-[11px] leading-snug text-ink-muted">{r.note}</p>
                </div>
              ))}
            </div>
            <Tooltip label="Weighted from the four dimensions above" withArrow>
              <div className="mt-4 flex items-baseline justify-between border-t border-hair pt-3">
                <span className="text-[12.5px] text-ink-2">Task score</span>
                <span className="text-[24px] font-semibold leading-none tracking-tight text-ink">73</span>
              </div>
            </Tooltip>
          </Panel>
        </div>
      </div>
    </div>
  )
}
