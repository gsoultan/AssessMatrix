/**
 * Tier 2's camera check, entirely on the device (docs/02 §3, DPIA in
 * docs/10-dpia-camera.md). About once a second the front camera's frame goes
 * through a small face detector in a worker of this tab; what leaves the device is only
 * the outcome, as numbers: "no face for 40 s", "two faces for 6 s". No frame,
 * picture, embedding or landmark is kept or sent.
 *
 * Loaded only for a tier 2 attempt, so the detector's ~4 MB never reaches a
 * quiz taker.
 */
import type { Signal } from './signals'

/** Where the runner build put the detector's runtime and model (vite.runner.config.ts). */
declare const __AM_VISION__: string
const BASE = typeof __AM_VISION__ === 'string' ? __AM_VISION__ : '/take/vision'

const EVERY_MS = 1000
/** Shorter than this is a sneeze or a glance down at paper, not a signal. */
const MISSING_MIN_MS = 3000
const MULTIPLE_MIN_MS = 1000
/** An ongoing stretch is reported in pieces, so the live monitor sees it without waiting for its end. */
const REPORT_EVERY_MS = 60_000

export type VisionState = 'starting' | 'on' | 'off'

type Out = { type: 'ready' } | { type: 'failed'; why: string } | { type: 'faces'; faces: number; at: number }

let worker: Worker | null = null
let loading: Promise<Worker> | null = null

/** Fetch and compile the detector ahead of time (on the readiness screen), in its worker, so the start is instant. */
export function preload(): Promise<Worker> {
  loading ??= new Promise<Worker>((resolve, reject) => {
    worker ??= new Worker(new URL('./vision.worker.ts', import.meta.url), { type: 'module' })
    const w = worker
    const done = (e: MessageEvent<Out>) => {
      if (e.data.type === 'faces') return
      w.removeEventListener('message', done)
      if (e.data.type === 'ready') resolve(w)
      else reject(new Error(e.data.why))
    }
    w.addEventListener('message', done)
    w.addEventListener('error', () => reject(new Error('the face check worker failed')), { once: true })
    w.postMessage({ type: 'load', base: BASE })
  })
  loading.catch(() => {
    loading = null
    worker?.terminate()
    worker = null
  })
  return loading
}

/** Is there a camera this page may use? Asks for permission; releases the camera straight away. */
export async function cameraReady(): Promise<'ok' | 'denied' | 'none'> {
  if (!navigator.mediaDevices?.getUserMedia) return 'none'
  try {
    const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'user' } })
    for (const t of s.getTracks()) t.stop()
    return 'ok'
  } catch (e) {
    return (e as DOMException)?.name === 'NotAllowedError' ? 'denied' : 'none'
  }
}

export type Camera = {
  /** Report stretches still under way (before a submit), and keep watching. */
  flush: () => void
  stop: () => void
}

/**
 * Watch the camera until stopped. `state` reports whether it is running, for
 * the candidate's "camera on" indicator.
 */
export function watchCamera(emit: (s: Signal) => void, state: (s: VisionState) => void): Camera {
  let stopped = false
  let tick = 0
  let stream: MediaStream | null = null
  let detector: Worker | null = null
  let busy = false
  const video = document.createElement('video')
  video.muted = true
  video.playsInline = true
  // In the page but not seen: a detached video stops getting frames in Chrome.
  // No self-view is shown, and nothing is drawn or kept.
  video.setAttribute('aria-hidden', 'true')
  Object.assign(video.style, { position: 'fixed', width: '1px', height: '1px', opacity: '0', pointerEvents: 'none', left: '0', top: '0' })
  document.body.append(video)

  // Stretches being measured: since when, and (for several faces) the most seen.
  let missingAt = 0
  let multipleAt = 0
  let most = 0

  const endMissing = (now: number) => {
    if (missingAt && now - missingAt >= MISSING_MIN_MS)
      emit({ kind: 'vision.face_missing', values: [String(now - missingAt)] })
    missingAt = 0
  }
  const endMultiple = (now: number) => {
    if (multipleAt && now - multipleAt >= MULTIPLE_MIN_MS)
      emit({ kind: 'vision.faces_multiple', values: [String(now - multipleAt), String(most)] })
    multipleAt = 0
    most = 0
  }
  /** `why` is a code the server turns into the sentence the candidate reads (domain.CameraStopped). */
  const quit = (why: 'permission_refused' | 'no_camera' | 'camera_ended' | 'check_failed' | 'check_unavailable') => {
    if (stopped) return
    const now = Date.now()
    endMissing(now)
    endMultiple(now)
    emit({ kind: 'vision.stopped', detail: why })
    halt()
  }
  const halt = () => {
    stopped = true
    detector?.removeEventListener('message', seen)
    window.clearInterval(tick)
    for (const t of stream?.getTracks() ?? []) t.stop()
    video.srcObject = null
    video.remove()
    state('off')
  }

  const look = () => {
    if (stopped || !detector || busy || video.readyState < 2) return
    // A hidden tab is already on the record as focus.lost; the camera is not the witness for it.
    if (document.visibilityState === 'hidden') {
      const now = Date.now()
      endMissing(now)
      endMultiple(now)
      return
    }
    busy = true
    const at = Date.now()
    createImageBitmap(video)
      .then((bitmap) => detector?.postMessage({ type: 'frame', bitmap, at }, [bitmap]))
      .catch(() => (busy = false))
  }
  const seen = (e: MessageEvent<Out>) => {
    if (stopped) return
    if (e.data.type === 'failed') return quit('check_failed')
    if (e.data.type !== 'faces') return
    busy = false
    const { faces, at: now } = e.data
    if (faces === 0) missingAt ||= now
    else endMissing(now)
    if (faces > 1) {
      multipleAt ||= now
      most = Math.max(most, faces)
    } else endMultiple(now)
    if (missingAt && now - missingAt >= REPORT_EVERY_MS) {
      endMissing(now)
      missingAt = now
    }
    if (multipleAt && now - multipleAt >= REPORT_EVERY_MS) {
      const keep = most
      endMultiple(now)
      multipleAt = now
      most = keep
    }
  }

  state('starting')
  void (async () => {
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'user', width: 320, height: 240 } })
    } catch (e) {
      return quit((e as DOMException)?.name === 'NotAllowedError' ? 'permission_refused' : 'no_camera')
    }
    if (stopped) return halt()
    for (const t of stream.getVideoTracks()) t.addEventListener('ended', () => quit('camera_ended'))
    video.srcObject = stream
    try {
      await video.play()
      detector = await preload()
    } catch {
      return quit('check_unavailable')
    }
    if (stopped) return halt()
    detector.addEventListener('message', seen)
    state('on')
    tick = window.setInterval(look, EVERY_MS)
  })()

  const flush = () => {
    if (stopped) return
    const now = Date.now()
    const [m, n, k] = [missingAt, multipleAt, most]
    endMissing(now)
    endMultiple(now)
    // Still under way: carry on measuring from here.
    if (m) missingAt = now
    if (n) [multipleAt, most] = [now, k]
  }
  return {
    flush,
    stop: () => {
      flush()
      if (!stopped) halt()
    },
  }
}
