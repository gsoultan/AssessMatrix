/**
 * What a candidate did, on its way to the server.
 *
 * Every answer becomes an event in a queue kept in localStorage, so a dropped
 * connection or a killed browser loses nothing that reached the queue. The
 * queue drains in batches; each batch gets its id before it is sent and keeps
 * it until the server acknowledges it, so a retry after a lost response is
 * recognised by the server and never appended twice.
 *
 * This is the simple version of docs/03's runner queue. Phase 4 moves it to
 * OPFS behind a worker; the batch protocol stays the same.
 */
import { ConnectError, Code } from '@connectrpc/connect'
import { api } from '../api/client'
import type { Attempt } from '../store'
import { type OutEvent, mediaOut } from './convert'
import type { Media } from '../media'

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

type Queue = {
  events: Pending[]
  inflight?: { batchId: string; events: Pending[] }
  /** After a refused batch, send this many events one at a time to find the bad one. */
  isolate?: number
}

const MAX_BATCH = 500
const key = (attemptId: string) => `am.pending.v1.${attemptId}`

function read(attemptId: string): Queue {
  try {
    return (JSON.parse(localStorage.getItem(key(attemptId)) ?? 'null') as Queue | null) ?? { events: [] }
  } catch {
    return { events: [] }
  }
}

function write(attemptId: string, q: Queue) {
  if (q.events.length === 0 && !q.inflight) localStorage.removeItem(key(attemptId))
  else localStorage.setItem(key(attemptId), JSON.stringify(q))
}

/** Events not yet acknowledged by the server. */
export const pendingCount = (attemptId: string) => {
  const q = read(attemptId)
  return q.events.length + (q.inflight?.events.length ?? 0)
}

/** Every unacknowledged event, oldest first. */
export const pendingEvents = (attemptId: string) => {
  const q = read(attemptId)
  return [...(q.inflight?.events ?? []), ...q.events]
}

export function enqueue(attemptId: string, ev: Pending) {
  const q = read(attemptId)
  // Typing is one event per pause, not per keystroke: a newer text for the same
  // item replaces one still waiting, since each carries the whole answer.
  const last = q.events[q.events.length - 1]
  if (ev.kind === 'response.typed' && last?.kind === 'response.typed' && last.itemId === ev.itemId) q.events[q.events.length - 1] = ev
  else q.events.push(ev)
  write(attemptId, q)
}

export function forget(attemptId: string) {
  localStorage.removeItem(key(attemptId))
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

async function drain(attemptId: string): Promise<Flushed> {
  let last: Flushed = { ok: true, serverNow: Date.now() }
  for (;;) {
    const q = read(attemptId)
    if (!q.inflight) {
      if (q.events.length === 0) return last
      const size = q.isolate ? 1 : MAX_BATCH
      q.inflight = { batchId: batchId(), events: q.events.slice(0, size) }
      q.events = q.events.slice(size)
      if (q.isolate) q.isolate--
      write(attemptId, q)
    }
    const batch = q.inflight
    try {
      const r = await api.candidate.appendEvents({ attemptId, batchId: batch.batchId, events: batch.events.map(toWire) })
      const after = read(attemptId)
      if (after.inflight?.batchId === batch.batchId) delete after.inflight
      write(attemptId, after)
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
          // it. Resend its events one by one so only the bad one is dropped.
          const after = read(attemptId)
          delete after.inflight
          if (batch.events.length > 1) {
            after.events = [...batch.events, ...after.events]
            after.isolate = batch.events.length
          } else console.warn('dropped an event the server refused:', batch.events[0]?.kind, err.rawMessage)
          write(attemptId, after)
          continue
        }
      }
      return { ok: false, why: 'offline' }
    }
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
