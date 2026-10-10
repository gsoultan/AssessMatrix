/**
 * One attempt, one tab.
 *
 * Two tabs on one attempt would each queue answers the other cannot see, and
 * whichever synced last would win. The first tab to open a sitting holds a
 * Web Lock named for it until the tab closes; a second tab is refused and
 * tells the first, which records it on the attempt for whoever reviews it.
 *
 * Web Locks rather than a SharedWorker: Chrome on Android, where most of our
 * candidates are, has no SharedWorker. A lock is released by the browser when
 * its tab dies, so a crashed tab never strands the sitting.
 */

const CHANNEL = 'am.seat'

let held: string | null = null
let release: (() => void) | undefined
let onRefused: ((key: string) => void) | undefined

const channel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(CHANNEL) : null
channel?.addEventListener('message', (ev: MessageEvent<{ refused?: string }>) => {
  if (ev.data.refused && ev.data.refused === held) onRefused?.(held)
})

/** Called in the holding tab when another tab tried to open the same sitting. */
export function whenRefused(fn: (key: string) => void) {
  onRefused = fn
}

/** True if this tab now holds the sitting; false if another tab does. */
export function claim(key: string): Promise<boolean> {
  if (held === key) return Promise.resolve(true)
  if (!navigator.locks) return Promise.resolve(true)
  leave()
  return new Promise((resolve) => {
    void navigator.locks.request(`am.seat.${key}`, { ifAvailable: true }, (lock) => {
      if (!lock) {
        channel?.postMessage({ refused: key })
        resolve(false)
        return
      }
      held = key
      resolve(true)
      return new Promise<void>((r) => {
        release = r
      })
    })
  })
}

/** Let the sitting go, as when the session ends in this tab. */
export function leave() {
  release?.()
  release = undefined
  held = null
}
