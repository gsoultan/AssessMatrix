import type { Severity } from '../data'

/* Severity → fill + track. The track is always a lighter step of the fill's own
   hue, so state reads across the whole bar (dataviz: marks-and-anatomy). */
const SEV: Record<Severity, { fill: string; track: string; word: string }> = {
  ok: { fill: 'var(--color-s1)', track: 'var(--color-track)', word: 'Normal' },
  warning: { fill: 'var(--color-warn)', track: 'var(--color-track-warn)', word: 'Warning' },
  serious: { fill: 'var(--color-serious)', track: 'var(--color-track-serious)', word: 'Serious' },
  critical: { fill: 'var(--color-critical)', track: 'var(--color-track-critical)', word: 'Critical' },
}

/** 12-point sparkline: de-emphasis hue for the series, accent for current period. */
export function Sparkline({ data, w = 84, h = 24 }: { data: number[]; w?: number; h?: number }) {
  const min = Math.min(...data)
  const max = Math.max(...data)
  const span = max - min || 1
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * (w - 4) + 2
    const y = h - 3 - ((v - min) / span) * (h - 6)
    return [x, y] as const
  })
  const last = pts[pts.length - 1]!
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-hidden="true" className="overflow-visible">
      <polyline
        points={pts.map(([x, y]) => `${x},${y}`).join(' ')}
        fill="none"
        stroke="var(--color-rule)"
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* 2px surface ring keeps the accent mark legible where it overlaps the line */}
      <circle cx={last[0]} cy={last[1]} r={4.5} fill="var(--color-surface)" />
      <circle cx={last[0]} cy={last[1]} r={3} fill="var(--color-s1)" />
    </svg>
  )
}

/**
 * Stat tile. label · value · optional delta (signed, vs a named period,
 * coloured by direction × whether up is good) · optional 12-point trend.
 */
export function StatTile({
  label,
  value,
  delta,
  goodWhenDown = false,
  spark,
  period = 'vs last week',
}: {
  label: string
  value: string
  delta?: number
  goodWhenDown?: boolean
  spark?: number[]
  period?: string
}) {
  const up = (delta ?? 0) >= 0
  const good = goodWhenDown ? !up : up
  const deltaColor = delta === undefined ? '' : good ? 'text-good-text' : 'text-[var(--color-critical)]'
  return (
    <div className="hair rounded-xl bg-surface p-4">
      <p className="text-[12px] leading-none text-ink-2">{label}</p>
      <div className="mt-2 flex items-end justify-between gap-3">
        <div>
          <p className="text-[28px] font-semibold leading-none tracking-tight text-ink">{value}</p>
          {delta !== undefined &&
            (delta === 0 ? (
              /* A zero delta is not an increase. Never draw a green up-arrow on no change. */
              <p className="mt-1.5 text-[12px] leading-none text-ink-muted">No change {period}</p>
            ) : (
              <p className={`mt-1.5 text-[12px] leading-none ${deltaColor}`}>
                {up ? '▲' : '▼'} {Math.abs(delta).toFixed(1)}% <span className="text-ink-muted">{period}</span>
              </p>
            ))}
        </div>
        {spark && <Sparkline data={spark} />}
      </div>
    </div>
  )
}

/**
 * Meter. Fill carries severity; track is a lighter step of the same hue.
 * A visible numeric label is always rendered — the palette's contrast WARN
 * obligates relief, and "what number is this" is the first question anyway.
 */
export function Meter({
  value,
  sev = 'ok',
  label,
  max = 100,
  showWord = false,
}: {
  value: number
  sev?: Severity
  label?: string
  max?: number
  showWord?: boolean
}) {
  const s = SEV[sev]
  const pct = Math.max(0, Math.min(100, (value / max) * 100))
  return (
    <div>
      {(label || showWord) && (
        <div className="mb-1.5 flex items-baseline justify-between gap-2">
          {label && <span className="text-[12px] text-ink-2">{label}</span>}
          <span className="tnum text-[12px] font-medium text-ink">
            {value}
            {max === 100 ? '' : `/${max}`}
            {showWord && <span className="ml-1.5 font-normal text-ink-muted">{s.word}</span>}
          </span>
        </div>
      )}
      <div
        className="h-2 w-full overflow-hidden rounded-full"
        style={{ background: s.track }}
        role="meter"
        aria-valuenow={value}
        aria-valuemin={0}
        aria-valuemax={max}
        aria-label={label ?? 'meter'}
      >
        <div className="h-full rounded-full" style={{ width: `${pct}%`, background: s.fill }} />
      </div>
    </div>
  )
}

/** Skill evidence bar: level + confidence interval, because a point estimate lies. */
export function SkillBar({
  name,
  level,
  ci,
  n,
  sev = 'ok',
}: {
  name: string
  level: number
  ci: number
  n: number
  sev?: Severity
}) {
  const s = SEV[sev]
  const lo = Math.max(0, level - ci)
  const hi = Math.min(100, level + ci)
  return (
    <div className="py-2.5">
      <div className="mb-1.5 flex items-baseline justify-between gap-3">
        <span className="truncate text-[13px] text-ink">{name}</span>
        <span className="tnum shrink-0 text-[12px] text-ink-2">
          {level}
          <span className="text-ink-muted"> ±{ci} · n={n}</span>
        </span>
      </div>
      <div className="relative h-2.5 w-full rounded-full" style={{ background: s.track }}>
        {/* confidence band */}
        <div
          className="absolute inset-y-0 rounded-full opacity-60"
          style={{ left: `${lo}%`, width: `${hi - lo}%`, background: s.fill }}
        />
        {/* point estimate, 2px surface ring so it reads against the band */}
        <div
          className="absolute top-1/2 h-4 w-1.5 -translate-x-1/2 -translate-y-1/2 rounded-sm"
          style={{ left: `${level}%`, background: s.fill, boxShadow: '0 0 0 2px var(--color-surface)' }}
        />
      </div>
    </div>
  )
}

export function StatusDot({ sev, label }: { sev: Severity; label: string }) {
  const s = SEV[sev]
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: s.fill }} />
      <span className="text-[12px] text-ink-2">{label}</span>
    </span>
  )
}
