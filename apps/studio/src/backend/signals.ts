/**
 * Integrity signals from the browser (docs/02 §3). Each one is a fact the
 * candidate could check for themselves: how long the exam was out of view,
 * how many characters were pasted, how long a question was on screen. Never
 * the pasted text, never what was on another tab, never a picture.
 *
 * Numbers go in `values` so the server's rules never parse prose. Only what
 * the tier collects is emitted; the server drops anything else regardless.
 */
import { collects } from '../integrity'

export type Signal = { kind: string; itemId?: string; values?: string[]; detail?: string }
type Emit = (s: Signal) => void

/** A glance shorter than this is not a signal (an OS notification, a mis-click). */
const AWAY_MIN_MS = 1000
/** A question flicked past in less than this was not "viewed". */
const VIEW_MIN_MS = 300

/**
 * Start watching. Returns a stop function. `item` says which question is on
 * screen now, for paste events.
 */
export function watch(tier: number, emit: Emit, item: () => string | undefined): () => void {
  const send: Emit = (s) => {
    if (collects(tier, s.kind)) emit(s)
  }
  const off: (() => void)[] = []
  const on = <K extends string>(target: EventTarget, type: K, fn: (e: Event) => void, capture = false) => {
    target.addEventListener(type, fn, capture)
    off.push(() => target.removeEventListener(type, fn, capture))
  }

  void device().then((d) => d && send({ kind: 'device.seen', values: [d.hash, d.kind], detail: d.kind }))

  // Out of view: hidden tab, minimised window, or focus in another app.
  let awayAt = 0
  let lostSent = false
  let lostTimer = 0
  const gone = (why: string) => {
    if (awayAt) return
    awayAt = Date.now()
    // Recorded only once it has lasted, so a notification does not count.
    lostTimer = window.setTimeout(() => {
      lostSent = true
      send({ kind: 'focus.lost', detail: why })
    }, AWAY_MIN_MS)
  }
  const back = () => {
    if (!awayAt || document.visibilityState !== 'visible' || !document.hasFocus()) return
    const ms = Date.now() - awayAt
    window.clearTimeout(lostTimer)
    if (lostSent) send({ kind: 'focus.returned', values: [String(ms)] })
    awayAt = 0
    lostSent = false
  }
  on(document, 'visibilitychange', () => (document.visibilityState === 'hidden' ? gone('tab hidden') : back()))
  on(window, 'blur', () => gone('window lost focus'))
  on(window, 'focus', back)

  // Pasted into an answer: how much, never what.
  on(
    document,
    'paste',
    (e) => {
      const chars = (e as ClipboardEvent).clipboardData?.getData('text')?.length ?? 0
      const id = item()
      if (chars > 0 && id) send({ kind: 'answer.pasted', itemId: id, values: [String(chars)] })
    },
    true,
  )

  if (tier >= 2) {
    let was = !!document.fullscreenElement
    on(document, 'fullscreenchange', () => {
      const now = !!document.fullscreenElement
      if (was && !now) send({ kind: 'fullscreen.exited' })
      was = now
    })
  }

  return () => {
    window.clearTimeout(lostTimer)
    for (const f of off.splice(0)) f()
  }
}

/**
 * Time on each question. Call with the question now on screen (or undefined
 * when none is); the previous one's time is emitted when it leaves.
 */
export function viewer(tier: number, emit: Emit): (itemId: string | undefined) => void {
  let current: string | undefined
  let since = 0
  return (next) => {
    if (next === current) return
    const now = Date.now()
    if (current && now - since >= VIEW_MIN_MS && collects(tier, 'item.viewed'))
      emit({ kind: 'item.viewed', itemId: current, values: [String(now - since)] })
    current = next
    since = now
  }
}

/** Ask for full screen (needs the click that starts the attempt). A refusal is not an error: it is recorded only if it is later left. */
export async function enterFullscreen() {
  try {
    if (!document.fullscreenElement) await document.documentElement.requestFullscreen?.({ navigationUI: 'hide' })
  } catch {
    // Some browsers (iOS Safari) have no element full screen. Nothing to record.
  }
}

/**
 * Which device this is, as a one-way hash: enough to see that an attempt
 * moved to a different device, not enough to say which device it was.
 */
async function device(): Promise<{ hash: string; kind: string } | null> {
  const touch = navigator.maxTouchPoints > 0
  const small = Math.min(screen.width, screen.height) < 600
  const kind = touch ? (small ? 'phone' : 'tablet') : 'desktop'
  const parts = [
    navigator.userAgent,
    navigator.language,
    Intl.DateTimeFormat().resolvedOptions().timeZone,
    `${screen.width}x${screen.height}@${devicePixelRatio}`,
    String(navigator.hardwareConcurrency ?? ''),
  ].join('|')
  try {
    const sum = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(parts))
    const hash = Array.from(new Uint8Array(sum).slice(0, 8), (b) => b.toString(16).padStart(2, '0')).join('')
    return { hash, kind }
  } catch {
    return null // crypto.subtle needs a secure context; better no signal than a false "new device"
  }
}
