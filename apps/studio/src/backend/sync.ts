/**
 * What a candidate did, on its way to the server.
 *
 * Every answer becomes an event in a queue kept in localStorage, so a dropped
 * connection or a killed browser loses nothing that reached the queue. The
 * queue drains in batches; each batch gets its id before it is sent and keeps
 * it until the server acknowledges it, so a retry after a lost response is
 * recognised by the server and never appended twice.
 *
 * The queue lives in memory for reads and on the device's shelf (shelf.ts:
 * OPFS behind a worker, else localStorage) for keeps. Every change is saved;
 * a batch is not sent until the save that created it is on disk, so a kill at
 * any moment leaves either the events or their acknowledged batch, never a
 * batch the server may have but the device forgot it sent.
 *
 * A send seals everything waiting into batches first, and a sealed batch
 * never changes. So what is on disk is always a row of batches the service
 * worker can send as they are, when the tab is gone (Background Sync, in
 * vite.runner.config.ts), without writing anything back: the server
 * recognises each batch id, whoever sends it first. The two senders take
 * turns on one Web Lock, and both send from the front of the row.
 */
import { ConnectError, Code } from '@connectrpc/connect'
import { api } from '../api/client'
import type { Attempt } from '../store'
import { type OutEvent, mediaOut } from './convert'
import type { Media } from '../media'
import { localShelf, shelf } from './shelf'

/** Answer-changing events carry the whole answer, so replaying the last one is enough. */
export type Pending = {
  kind: string
  itemId?: string
  values?: string[]
  text?: string
  file?: Media
  detail?: string
  clientTime: number
}

type Batch = { batchId: string; events: Pending[] }

type Queue = {
  /** Not yet in a batch. */
  events: Pending[]
  /** Sealed, oldest first: each keeps its id until the server acknowledges it. */
  batches?: Batch[]
}

/** A queue saved by a version that had one batch in flight at a time. */
type Legacy = Queue & { inflight?: Batch; isolate?: number }

const MAX_BATCH = 500
const key = (attemptId: string) => `am.pending.v1.${attemptId}`

const mem = new Map<string, Queue>()
const saves = new Map<string, Promise<void>>()
const due = new Set<string>()

/**
 * Events made before the queue was read off the device. They join it once it
 * is; until then a write would replace what is on disk with less.
 */
const early = new Map<string, Pending[]>()
const opening = new Map<string, Promise<void>>()

const read = (attemptId: string): Queue => {
  const q = structuredClone(mem.get(attemptId) ?? { events: [] })
  return mem.has(attemptId) ? q : { ...q, events: [...q.events, ...(early.get(attemptId) ?? [])] }
}

function write(attemptId: string, q: Queue) {
  if (!mem.has(attemptId)) throw new Error('sync: write before open')
  mem.set(attemptId, q)
  // Saves for one attempt run in order, and a save writes whatever is newest
  // when it starts, so a burst of changes costs one or two writes.
  if (due.has(attemptId)) return
  due.add(attemptId)
  const prev = saves.get(attemptId) ?? Promise.resolve()
  const next = prev.then(async () => {
    due.delete(attemptId)
    const now = mem.get(attemptId)
    const text = !now || (now.events.length === 0 && !now.batches?.length) ? null : JSON.stringify(now)
    const s = await shelf()
    try {
      await s.save(key(attemptId), text)
    } catch (err) {
      // A shelf that fails mid-session (quota, a revoked handle): the
      // fallback is better than keeping it in memory only.
      console.warn('queue save fell back to localStorage:', err)
      await localShelf.save(key(attemptId), text)
    }
  })
  saves.set(attemptId, next)
}

/** Resolves once every change so far is on the device. */
export const durable = (attemptId: string) => saves.get(attemptId) ?? Promise.resolve()

/**
 * Read an attempt's queue off the device. Call before reading or changing it;
 * the gate does, before it hands over a seat.
 */
export function open(attemptId: string): Promise<void> {
  if (mem.has(attemptId)) return Promise.resolve()
  let p = opening.get(attemptId)
  if (!p) {
    p = load(attemptId).finally(() => opening.delete(attemptId))
    opening.set(attemptId, p)
  }
  return p
}

async function load(attemptId: string) {
  const s = await shelf()
  let text = await s.load(key(attemptId)).catch(() => null)
  // A queue written by an earlier version, or by the fallback.
  const legacy = s.kind === 'opfs' ? localStorage.getItem(key(attemptId)) : null
  if (!text && legacy) text = legacy
  let q: Queue = { events: [] }
  try {
    if (text) {
      const { inflight, isolate: _, ...rest } = JSON.parse(text) as Legacy
      q = { ...rest, batches: [...(inflight ? [inflight] : []), ...(rest.batches ?? [])] }
    }
  } catch {
    /* unreadable: nothing to resend */
  }
  // Forgotten while it was loading: what was on disk is already discarded.
  if (mem.has(attemptId)) return
  const before = early.get(attemptId) ?? []
  early.delete(attemptId)
  mem.set(attemptId, { ...q, events: [...q.events, ...before] })
  if (before.length || legacy) {
    write(attemptId, mem.get(attemptId)!)
    await durable(attemptId)
    if (legacy) localStorage.removeItem(key(attemptId))
  }
}

/** Events not yet acknowledged by the server. */
export const pendingCount = (attemptId: string) => pendingEvents(attemptId).length

/** Every unacknowledged event, oldest first. */
export const pendingEvents = (attemptId: string) => {
  const q = read(attemptId)
  return [...(q.batches ?? []).flatMap((b) => b.events), ...q.events]
}

export function enqueue(attemptId: string, ev: Pending) {
  if (!mem.has(attemptId)) {
    early.set(attemptId, [...(early.get(attemptId) ?? []), ev])
    void open(attemptId)
    return
  }
  const q = read(attemptId)
  // Typing is one event per pause, not per keystroke: a newer text for the same
  // item replaces one still waiting, since each carries the whole answer.
  const last = q.events[q.events.length - 1]
  if (ev.kind === 'response.typed' && last?.kind === 'response.typed' && last.itemId === ev.itemId) q.events[q.events.length - 1] = ev
  else q.events.push(ev)
  write(attemptId, q)
}

export function forget(attemptId: string) {
  early.delete(attemptId)
  mem.set(attemptId, { events: [] })
  write(attemptId, { events: [] })
}

const batchId = () => {
  const b = crypto.getRandomValues(new Uint8Array(16))
  return [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
}

const toWire = (e: Pending): OutEvent => ({
  kind: e.kind,
  itemId: e.itemId ?? '',
  values: e.values ?? [],
  text: e.text ?? '',
  file: mediaOut(e.file),
  detail: e.detail ?? '',
  clientTime: BigInt(e.clientTime),
})

export type Flushed =
  | { ok: true; serverNow: number; deadlineAt?: number }
  /** Nothing reached the server; the queue is intact and will be retried. */
  | { ok: false; why: 'offline' }
  /** The session is gone: revoked, or replaced on another device. */
  | { ok: false; why: 'session' }
  /** The attempt is closed to new events (already submitted). */
  | { ok: false; why: 'closed' }

const running = new Map<string, Promise<Flushed>>()

/** Send everything queued. Concurrent calls for one attempt share one drain. */
export function flush(attemptId: string): Promise<Flushed> {
  const busy = running.get(attemptId)
  if (busy) return busy.then(() => drain(attemptId))
  const p = drain(attemptId).finally(() => running.delete(attemptId))
  running.set(attemptId, p)
  return p
}

/**
 * One sender at a time per attempt, across this tab and the service worker,
 * holding on from the send until its acknowledgement is on disk: the worker
 * then never reads a row that the server has already taken part of.
 */
const turn = <T>(attemptId: string, fn: () => Promise<T>): Promise<T> =>
  typeof navigator !== 'undefined' && navigator.locks ? navigator.locks.request(`am.send.${attemptId}`, fn) : fn()

async function drain(attemptId: string): Promise<Flushed> {
  await open(attemptId)
  let last: Flushed = { ok: true, serverNow: Date.now() }
  for (;;) {
    const q = read(attemptId)
    if (q.events.length) {
      const batches = [...(q.batches ?? [])]
      for (let i = 0; i < q.events.length; i += MAX_BATCH) batches.push({ batchId: batchId(), events: q.events.slice(i, i + MAX_BATCH) })
      write(attemptId, { ...q, events: [], batches })
      // The batch id must be on disk before the batch leaves: a retry after a
      // kill then reuses it, and the server recognises it.
      await durable(attemptId)
      continue
    }
    const batch = q.batches?.[0]
    if (!batch) return last
    try {
      const r = await turn(attemptId, async () => {
        const r = await api.candidate.appendEvents({ attemptId, batchId: batch.batchId, events: batch.events.map(toWire) })
        const after = read(attemptId)
        if (after.batches?.[0]?.batchId === batch.batchId) after.batches.shift()
        write(attemptId, after)
        await durable(attemptId)
        return r
      })
      last = { ok: true, serverNow: Number(r.serverNow), deadlineAt: Number(r.deadlineAt) || undefined }
    } catch (err) {
      if (err instanceof ConnectError) {
        if (err.code === Code.Unauthenticated) return { ok: false, why: 'session' }
        if (err.code === Code.FailedPrecondition) {
          // Submitted elsewhere or already: nothing more can be added.
          forget(attemptId)
          return { ok: false, why: 'closed' }
        }
        if (err.code === Code.InvalidArgument) {
          // A batch the server will never take would block everything behind
          // it. It stored none of it, so its events go again one per batch,
          // and only the bad one is dropped.
          const after = read(attemptId)
          const rest = (after.batches ?? []).filter((b) => b.batchId !== batch.batchId)
          if (batch.events.length > 1) rest.unshift(...batch.events.map((e) => ({ batchId: batchId(), events: [e] })))
          else console.warn('dropped an event the server refused:', batch.events[0]?.kind, err.rawMessage)
          write(attemptId, { ...after, batches: rest })
          await durable(attemptId)
          continue
        }
      }
      void later()
      return { ok: false, why: 'offline' }
    }
  }
}

/**
 * Ask the service worker to send what is waiting once the network is back,
 * even if this tab is closed by then. Only where there is one (the built
 * runner) and the browser has Background Sync (Chromium); elsewhere the queue
 * waits on the device for the sitting to be reopened, as before.
 */
async function later() {
  try {
    const reg = await navigator.serviceWorker?.getRegistration('/take/')
    await (reg as (ServiceWorkerRegistration & { sync?: { register(tag: string): Promise<void> } }) | undefined)?.sync?.register('am-queue')
  } catch {
    /* refused (no permission, a private window): the tab still retries */
  }
}

/** Lay unacknowledged events over what the server last said, so a reload shows the latest answers. */
export function replay(a: Attempt, events: Pending[]): Attempt {
  const answers = { ...a.answers }
  const text = { ...a.text }
  const files = { ...a.files }
  let flagged = [...a.flagged]
  for (const e of events) {
    const id = e.itemId
    if (!id) continue
    switch (e.kind) {
      case 'response.changed':
        answers[id] = e.values ?? []
        break
      case 'response.typed':
        text[id] = e.text ?? ''
        break
      case 'answer.attached':
        if (e.file) files[id] = e.file
        break
      case 'answer.detached':
        delete files[id]
        break
      case 'item.flagged':
        if (!flagged.includes(id)) flagged = [...flagged, id]
        break
      case 'item.unflagged':
        flagged = flagged.filter((f) => f !== id)
        break
    }
  }
  return { ...a, answers, text, files, flagged, queued: events.length }
}
