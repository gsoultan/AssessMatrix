/**
 * Just enough ZIP for the files this app exchanges: .xlsx rosters in, QTI
 * packages in and out. Reading handles stored and deflated entries through
 * the browser's own DecompressionStream; writing stores entries uncompressed,
 * which every reader accepts.
 *
 * Archives are other people's files, so every size they declare is a claim:
 * the cap is enforced on what actually inflates.
 */

export type ZipFault = 'not-zip' | 'damaged' | 'encrypted' | 'too-large' | 'method'

export class ZipError extends Error {
  constructor(readonly fault: ZipFault) {
    super(`zip: ${fault}`)
  }
}

export type ZipEntry = { name: string; method: number; packed: number; size: number; offset: number; encrypted: boolean }

/** The central directory: what is in the archive and where. */
export function zipEntries(buf: ArrayBuffer): ZipEntry[] {
  const v = new DataView(buf)
  // The end-of-directory record sits at the tail, behind an optional comment.
  let eocd = -1
  for (let i = buf.byteLength - 22; i >= Math.max(0, buf.byteLength - 22 - 0xffff); i--)
    if (v.getUint32(i, true) === 0x06054b50) {
      eocd = i
      break
    }
  if (eocd < 0) throw new ZipError('not-zip')
  const count = v.getUint16(eocd + 10, true)
  let at = v.getUint32(eocd + 16, true)
  if (count === 0xffff || at === 0xffffffff) throw new ZipError('too-large')

  const names = new TextDecoder()
  const out: ZipEntry[] = []
  for (let n = 0; n < count; n++) {
    if (at + 46 > buf.byteLength || v.getUint32(at, true) !== 0x02014b50) throw new ZipError('damaged')
    const nameLen = v.getUint16(at + 28, true)
    out.push({
      encrypted: (v.getUint16(at + 8, true) & 1) === 1,
      method: v.getUint16(at + 10, true),
      packed: v.getUint32(at + 20, true),
      size: v.getUint32(at + 24, true),
      offset: v.getUint32(at + 42, true),
      name: names.decode(new Uint8Array(buf, at + 46, nameLen)),
    })
    at += 46 + nameLen + v.getUint16(at + 30, true) + v.getUint16(at + 32, true)
  }
  return out
}

/** One entry's bytes, refusing to inflate past `cap`. */
export async function zipBytes(buf: ArrayBuffer, e: ZipEntry, cap: number): Promise<Uint8Array> {
  if (e.encrypted) throw new ZipError('encrypted')
  if (e.size > cap) throw new ZipError('too-large')
  const v = new DataView(buf)
  if (e.offset + 30 > buf.byteLength || v.getUint32(e.offset, true) !== 0x04034b50) throw new ZipError('damaged')
  const start = e.offset + 30 + v.getUint16(e.offset + 26, true) + v.getUint16(e.offset + 28, true)
  if (start + e.packed > buf.byteLength) throw new ZipError('damaged')
  const packed = new Uint8Array(buf, start, e.packed)
  if (e.method === 0) return packed
  if (e.method !== 8) throw new ZipError('method')

  const reader = new Blob([packed]).stream().pipeThrough(new DecompressionStream('deflate-raw')).getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    total += value.byteLength
    if (total > cap) {
      await reader.cancel()
      throw new ZipError('too-large')
    }
    chunks.push(value)
  }
  return new Uint8Array(await new Blob(chunks as BlobPart[]).arrayBuffer())
}

export const zipText = async (buf: ArrayBuffer, e: ZipEntry, cap: number) =>
  new TextDecoder().decode(await zipBytes(buf, e, cap))

const CRC = (() => {
  const t = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    t[n] = c >>> 0
  }
  return t
})()

function crc32(b: Uint8Array): number {
  let c = ~0
  for (let i = 0; i < b.length; i++) c = CRC[(c ^ b[i]!) & 0xff]! ^ (c >>> 8)
  return ~c >>> 0
}

/** A stored (uncompressed) archive of the given files, in order. */
export function writeZip(files: [string, Uint8Array][]): Blob {
  const enc = new TextEncoder()
  const parts: Uint8Array[] = []
  const central: Uint8Array[] = []
  let offset = 0
  for (const [name, data] of files) {
    const n = enc.encode(name)
    const crc = crc32(data)
    const head = new DataView(new ArrayBuffer(30))
    head.setUint32(0, 0x04034b50, true)
    head.setUint16(4, 20, true)
    head.setUint16(6, 0x0800, true) // names are UTF-8
    head.setUint32(14, crc, true)
    head.setUint32(18, data.length, true)
    head.setUint32(22, data.length, true)
    head.setUint16(26, n.length, true)
    const dir = new DataView(new ArrayBuffer(46))
    dir.setUint32(0, 0x02014b50, true)
    dir.setUint16(4, 20, true)
    dir.setUint16(6, 20, true)
    dir.setUint16(8, 0x0800, true)
    dir.setUint32(16, crc, true)
    dir.setUint32(20, data.length, true)
    dir.setUint32(24, data.length, true)
    dir.setUint16(28, n.length, true)
    dir.setUint32(42, offset, true)
    parts.push(new Uint8Array(head.buffer), n, data)
    central.push(new Uint8Array(dir.buffer), n)
    offset += 30 + n.length + data.length
  }
  const size = central.reduce((s, c) => s + c.length, 0)
  const end = new DataView(new ArrayBuffer(22))
  end.setUint32(0, 0x06054b50, true)
  end.setUint16(8, files.length, true)
  end.setUint16(10, files.length, true)
  end.setUint32(12, size, true)
  end.setUint32(16, offset, true)
  return new Blob([...parts, ...central, new Uint8Array(end.buffer)] as BlobPart[], { type: 'application/zip' })
}
