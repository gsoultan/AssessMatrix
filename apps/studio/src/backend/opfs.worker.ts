/// <reference lib="webworker" />
/**
 * The candidate's answer queue on disk, in the origin-private file system.
 *
 * Only a worker can open a synchronous access handle, and only that handle has
 * flush(): the one write in a browser that is on disk when it returns. That is
 * the point of this file. localStorage is written back lazily, so a browser
 * killed a moment after an answer can lose it.
 *
 * A save never overwrites the copy a load would read. Each key has two slots;
 * a save goes to the older one with a higher sequence number, and a load takes
 * the newest slot that reads back whole. A crash mid-write leaves the other
 * slot intact.
 */

type Req = { id: number; op: 'probe' } | { id: number; op: 'load'; key: string } | { id: number; op: 'save'; key: string; text: string | null }

const enc = new TextEncoder()

let dir: Promise<FileSystemDirectoryHandle> | undefined
const root = () => (dir ??= navigator.storage.getDirectory())

/** Slot contents: `<seq>\n<text>`, so a torn write is a bad sequence number or bad JSON. */
async function readSlot(name: string): Promise<{ seq: number; text: string } | null> {
  try {
    const fh = await (await root()).getFileHandle(name)
    const raw = await (await fh.getFile()).text()
    const nl = raw.indexOf('\n')
    const seq = Number(raw.slice(0, nl))
    const text = raw.slice(nl + 1)
    if (nl < 0 || !Number.isSafeInteger(seq)) return null
    if (text !== '') JSON.parse(text)
    return { seq, text }
  } catch {
    return null
  }
}

const slots = (key: string) => [`${key}.a`, `${key}.b`] as const

async function load(key: string): Promise<{ text: string | null; seq: number; older: string }> {
  const [a, b] = slots(key)
  const [ra, rb] = await Promise.all([readSlot(a), readSlot(b)])
  const newest = !ra ? rb : !rb ? ra : ra.seq >= rb.seq ? ra : rb
  const older = newest === ra ? b : a
  return { text: newest && newest.text !== '' ? newest.text : null, seq: newest?.seq ?? 0, older }
}

async function writeSlot(name: string, body: string) {
  const fh = await (await root()).getFileHandle(name, { create: true })
  const h = await fh.createSyncAccessHandle()
  try {
    const bytes = enc.encode(body)
    h.truncate(0)
    h.write(bytes, { at: 0 })
    h.flush()
  } finally {
    h.close()
  }
}

async function save(key: string, text: string | null) {
  const { seq, older } = await load(key)
  if (text === null) {
    // Empty, newest: an emptied queue must not resurrect from the other slot.
    await writeSlot(older, `${seq + 1}\n`)
    return
  }
  await writeSlot(older, `${seq + 1}\n${text}`)
}

async function probe() {
  const name = `probe-${crypto.randomUUID()}`
  await writeSlot(name, '1\n')
  await (await root()).removeEntry(name)
  return 'ok'
}

// One operation at a time: two sync handles on one file cannot coexist.
let chain: Promise<unknown> = Promise.resolve()

self.onmessage = (ev: MessageEvent<Req>) => {
  const r = ev.data
  chain = chain.then(async () => {
    try {
      const result = r.op === 'probe' ? await probe() : r.op === 'load' ? (await load(r.key)).text : await save(r.key, r.text)
      self.postMessage({ id: r.id, ok: true, result: result ?? null })
    } catch (e) {
      self.postMessage({ id: r.id, ok: false, error: String(e) })
    }
  })
}
