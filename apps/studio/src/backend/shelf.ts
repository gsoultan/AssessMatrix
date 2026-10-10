/**
 * Where the answer queue is kept on this device.
 *
 * The origin-private file system, through a worker that flushes every save to
 * disk before it reports done (opfs.worker.ts). Where that is unavailable (an
 * old browser, a private window that refuses OPFS) localStorage, which is
 * durable in practice but written back lazily.
 */

export interface Shelf {
  readonly kind: 'opfs' | 'local'
  load(key: string): Promise<string | null>
  /** Resolves once the value is on disk. null removes it. */
  save(key: string, text: string | null): Promise<void>
}

const local: Shelf = {
  kind: 'local',
  async load(key) {
    return localStorage.getItem(key)
  },
  async save(key, text) {
    if (text === null) localStorage.removeItem(key)
    else localStorage.setItem(key, text)
  },
}

function opfs(): Promise<Shelf | null> {
  if (typeof Worker === 'undefined' || !navigator.storage?.getDirectory) return Promise.resolve(null)
  let worker: Worker
  try {
    worker = new Worker(new URL('./opfs.worker.ts', import.meta.url), { type: 'module' })
  } catch {
    return Promise.resolve(null)
  }
  let next = 0
  const waiting = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>()
  worker.onmessage = (ev: MessageEvent<{ id: number; ok: boolean; result?: unknown; error?: string }>) => {
    const w = waiting.get(ev.data.id)
    waiting.delete(ev.data.id)
    if (ev.data.ok) w?.resolve(ev.data.result)
    else w?.reject(new Error(ev.data.error))
  }
  const call = (msg: object) =>
    new Promise<unknown>((resolve, reject) => {
      const id = ++next
      waiting.set(id, { resolve, reject })
      worker.postMessage({ id, ...msg })
    })
  const shelf: Shelf = {
    kind: 'opfs',
    load: (key) => call({ op: 'load', key }) as Promise<string | null>,
    save: async (key, text) => {
      await call({ op: 'save', key, text })
    },
  }
  // A worker that cannot reach OPFS (or never starts) fails the probe.
  const timeout = new Promise<null>((r) => setTimeout(() => r(null), 3000))
  return Promise.race([
    call({ op: 'probe' }).then(
      () => shelf,
      () => null,
    ),
    timeout,
  ]).then((s) => {
    if (!s) worker.terminate()
    return s
  })
}

let chosen: Promise<Shelf> | undefined

/** The shelf this browser can use, decided once. */
export const shelf = (): Promise<Shelf> => (chosen ??= opfs().then((s) => s ?? local))

/** For a test, or a browser that turned out to refuse OPFS mid-session. */
export const localShelf = local
