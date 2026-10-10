/**
 * The face detector, off the main thread: compiling it and running it are
 * the only heavy work in the runner, and a frozen tap reads as a lost answer.
 * Frames arrive as ImageBitmaps and leave as a face count; nothing is kept.
 */
import { FaceDetector, FilesetResolver } from '@mediapipe/tasks-vision'

type In = { type: 'load'; base: string } | { type: 'frame'; bitmap: ImageBitmap; at: number }

let detector: Promise<FaceDetector> | null = null

// The runtime reports routine start-up ("INFO: Created TensorFlow Lite XNNPACK
// delegate") through console.error; it is not an error, so it is not one here.
const report = console.error.bind(console)
console.error = (...a: unknown[]) => {
  if (typeof a[0] === 'string' && a[0].startsWith('INFO:')) return
  report(...a)
}

self.onmessage = async (e: MessageEvent<In>) => {
  const m = e.data
  if (m.type === 'load') {
    detector ??= (async () => {
      // The module flavour of the runtime: a module worker cannot importScripts.
      const files = await FilesetResolver.forVisionTasks(m.base, true)
      return FaceDetector.createFromOptions(files, {
        baseOptions: { modelAssetPath: `${m.base}/blaze_face_short_range.tflite`, delegate: 'CPU' },
        runningMode: 'VIDEO',
        minDetectionConfidence: 0.5,
      })
    })()
    try {
      await detector
      postMessage({ type: 'ready' })
    } catch (err) {
      detector = null
      postMessage({ type: 'failed', why: String(err) })
    }
    return
  }
  try {
    const d = await detector
    if (!d) throw new Error('not loaded')
    const faces = d.detectForVideo(m.bitmap, m.at).detections.length
    postMessage({ type: 'faces', faces, at: m.at })
  } catch (err) {
    postMessage({ type: 'failed', why: String(err) })
  } finally {
    m.bitmap.close()
  }
}
