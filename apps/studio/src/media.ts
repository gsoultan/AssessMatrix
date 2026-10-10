/**
 * Image ingest for the prototype.
 *
 * Production shape is unchanged: an item references media by URL and the bytes
 * live in object storage (docs/03-architecture.md §3). Here the URL happens to
 * be a `data:` URL so the prototype has no backend — swapping to real storage is
 * a change of field *value*, not of schema.
 *
 * Everything is downscaled and re-encoded on the way in. An exam with a dozen
 * untouched phone photos would blow the localStorage quota instantly, and a
 * 4 MB image on a mid-range Android in a regional school is its own failure.
 */

export type Media = {
  id: string
  url: string
  alt: string
  w: number
  h: number
  bytes: number
}

export const MAX_INPUT_BYTES = 12 * 1024 * 1024
const MAX_EDGE = 1400
const QUALITY = 0.85

export class MediaError extends Error {}

const rid = () => `m_${Math.random().toString(36).slice(2, 10)}`

export const prettyBytes = (n: number) =>
  n < 1024 ? `${n} B` : n < 1024 * 1024 ? `${(n / 1024).toFixed(0)} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`

function loadImage(file: File): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      URL.revokeObjectURL(url)
      resolve(img)
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new MediaError('That file could not be read as an image.'))
    }
    img.src = url
  })
}

/** File → downscaled, re-encoded Media. Never stores the original bytes. */
export async function ingestImage(file: File, alt = ''): Promise<Media> {
  if (!file.type.startsWith('image/')) throw new MediaError('Only image files can be attached.')
  if (file.size > MAX_INPUT_BYTES)
    throw new MediaError(`That image is ${prettyBytes(file.size)}. The limit is ${prettyBytes(MAX_INPUT_BYTES)}.`)

  const img = await loadImage(file)
  const scale = Math.min(1, MAX_EDGE / Math.max(img.naturalWidth, img.naturalHeight))
  const w = Math.max(1, Math.round(img.naturalWidth * scale))
  const h = Math.max(1, Math.round(img.naturalHeight * scale))

  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new MediaError('This browser could not process the image.')
  ctx.imageSmoothingQuality = 'high'
  ctx.drawImage(img, 0, 0, w, h)

  // WebP holds up on both photographs and diagrams with fine text, where JPEG
  // smears the text and PNG is far too large.
  let url = canvas.toDataURL('image/webp', QUALITY)
  if (!url.startsWith('data:image/webp')) url = canvas.toDataURL('image/jpeg', QUALITY)

  return { id: rid(), url, alt, w, h, bytes: Math.round((url.length - url.indexOf(',') - 1) * 0.75) }
}

/** Rough footprint of everything we are about to try to persist. */
export const mediaBytes = (...media: (Media | undefined)[]) =>
  media.reduce((n, m) => n + (m?.bytes ?? 0), 0)
