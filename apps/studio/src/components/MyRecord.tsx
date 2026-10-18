/**
 * The candidate's own copy of their record, from the core: every event it
 * holds about the attempt, whether the record still matches its seal, and a
 * download of all of it (UU PDP No. 27/2022, the right of access). Read only
 * when asked for, so the result screen does not wait on it.
 */
import { useState } from 'react'
import { IconDownload, IconShieldCheck, IconAlertTriangle } from '@tabler/icons-react'
import { toJson } from '@bufbuild/protobuf'
import type { AttemptEvent, GetRecordResponse } from '@assessmatrix/proto/candidate_pb'
import { GetRecordResponseSchema } from '@assessmatrix/proto/candidate_pb'
import { api } from '../api/client'
import { explain } from '../backend/repo'

const PURPOSE: Record<string, string> = {
  signals: 'catatan aktivitas',
  fullscreen: 'layar penuh',
  camera: 'pemeriksaan wajah oleh kamera',
}

const CAMERA_STOPPED: Record<string, string> = {
  permission_refused: 'izin kamera ditolak',
  no_camera: 'kamera tidak dapat dibuka',
  camera_ended: 'kamera dimatikan atau terputus',
  check_failed: 'pemeriksaan wajah gagal di perangkat ini',
  check_unavailable: 'pemeriksaan wajah tidak dapat dimuat di perangkat ini',
}

const secs = (v?: string) => `${Math.round(Number(v ?? 0) / 1000)} detik`

/** One event in a sentence, in the candidate's language. */
function say(e: AttemptEvent, numberOf: (id: string) => number): string {
  const v = e.values
  const q = () => {
    const n = numberOf(e.itemId)
    return n > 0 ? `soal ${n}` : 'soal yang tidak ada di versi ini'
  }
  const k = e.kind
  if (k.startsWith('consent.') && k.endsWith('.withdrawn'))
    return `Persetujuan ditarik: ${PURPOSE[k.slice(8, -10)] ?? k.slice(8, -10)}`
  if (k.startsWith('consent.')) return `Persetujuan diberikan: ${PURPOSE[k.slice(8)] ?? k.slice(8)}`
  switch (k) {
    case 'attempt.started':
      return 'Ujian dimulai'
    case 'identity.verified':
      return 'Identitas diperiksa: tautan pribadi dan kode yang dikirim ke email Anda'
    case 'attempt.reentered':
      return 'Masuk lagi ke ujian'
    case 'response.changed':
      return `Memilih jawaban ${q()}`
    case 'response.typed':
      return `Mengetik jawaban ${q()}`
    case 'answer.attached':
      return `Melampirkan berkas ke ${q()}`
    case 'answer.detached':
      return `Melepas berkas dari ${q()}`
    case 'item.flagged':
      return `Menandai ${q()} untuk dilihat lagi`
    case 'item.unflagged':
      return `Melepas tanda ${q()}`
    case 'item.viewed':
      return `Di ${q()} selama ${secs(v[0])}`
    case 'focus.lost':
      return 'Meninggalkan halaman ujian'
    case 'focus.returned':
      return `Kembali ke halaman ujian setelah ${secs(v[0])}`
    case 'answer.pasted':
      return `Menempelkan ${v[0] ?? '?'} karakter ke ${q()}`
    case 'device.seen':
      return `Perangkat: ${v[1] || 'tidak diketahui'}`
    case 'session.second_tab':
      return 'Tab kedua mencoba membuka pengerjaan ini dan ditolak'
    case 'fullscreen.exited':
      return 'Keluar dari layar penuh'
    case 'vision.face_missing':
      return `Tidak ada wajah di kamera selama ${secs(v[0])}`
    case 'vision.faces_multiple':
      return `${v[1] ?? 'Beberapa'} wajah di kamera selama ${secs(v[0])}`
    case 'vision.stopped':
      return `Pemeriksaan kamera berhenti: ${CAMERA_STOPPED[e.detail] ?? 'tanpa alasan yang dikenali'}`
    case 'attempt.submitted':
      return 'Diserahkan'
    case 'attempt.expired':
      return 'Waktu habis; diserahkan otomatis'
    case 'review.scored':
      return `Dinilai penilai: ${q()}, ${v[0] ?? '?'} poin`
  }
  return k
}

export function MyRecord({ numberOf }: { numberOf: (id: string) => number }) {
  const [rec, setRec] = useState<GetRecordResponse | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const load = async () => {
    setBusy(true)
    setError('')
    try {
      setRec(await api.candidate.getRecord({}))
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }

  const download = () => {
    if (!rec) return
    const blob = new Blob([JSON.stringify(toJson(GetRecordResponseSchema, rec), null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `catatan-ujian-${rec.attempt?.id.slice(0, 8) ?? 'saya'}.json`
    a.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  if (!rec)
    return (
      <div className="px-4 py-3">
        <p className="text-[12px] leading-snug text-ink-muted">
          Semua yang kami simpan tentang pengerjaan ini: setiap peristiwa, persetujuan Anda, dan penilaiannya. Tidak ada video
          atau foto.
        </p>
        <button
          onClick={load}
          disabled={busy}
          className="mt-2.5 h-9 rounded-lg px-3.5 text-[12.5px] font-medium text-ink disabled:opacity-50"
          style={{ background: 'var(--color-wash)' }}
        >
          {busy ? 'Memuat…' : 'Tampilkan catatan lengkap'}
        </button>
        {error && (
          <p className="mt-2 text-[12px]" style={{ color: 'var(--color-critical)' }}>
            {error}
          </p>
        )}
      </div>
    )

  const a = rec.attempt
  const events = a?.events ?? []
  const purged = Number(a?.purgedAt ?? 0)
  return (
    <div className="px-4 py-3">
      <p
        className="flex items-start gap-1.5 text-[12px] leading-snug"
        style={{ color: rec.chainIntact ? 'var(--color-good-text)' : 'var(--color-critical)' }}
      >
        {rec.chainIntact ? (
          <IconShieldCheck size={14} stroke={2} className="mt-px shrink-0" />
        ) : (
          <IconAlertTriangle size={14} stroke={2} className="mt-px shrink-0" />
        )}
        {rec.chainIntact
          ? 'Catatan ini utuh: cocok dengan segel yang dibuat saat setiap peristiwa disimpan.'
          : `Catatan ini tidak lagi cocok dengan segelnya mulai peristiwa ke-${rec.brokenAtSeq}. Sampaikan kepada penyelenggara.`}
      </p>
      {purged > 0 ? (
        <p className="mt-2 text-[12px] text-ink-2">
          Rincian peristiwa sudah dihapus pada {new Date(purged).toLocaleDateString('id-ID')} sesuai masa simpan. Skornya tetap.
        </p>
      ) : (
        <ol className="mt-2 max-h-[360px] overflow-y-auto" aria-label="Catatan lengkap">
          {events.map((e) => (
            <li key={String(e.seq)} className="tnum flex items-baseline justify-between gap-3 border-b border-hair py-1.5 text-[12px] last:border-0">
              <span className="text-ink-2">
                <span className="text-ink-muted">{String(e.seq)}.</span> {say(e, numberOf)}
              </span>
              <span className="shrink-0 text-ink-muted">{new Date(Number(e.serverTime)).toLocaleTimeString('id-ID')}</span>
            </li>
          ))}
        </ol>
      )}
      <button
        onClick={download}
        className="mt-3 inline-flex h-9 items-center gap-1.5 rounded-lg px-3.5 text-[12.5px] font-medium text-ink"
        style={{ background: 'var(--color-wash)' }}
      >
        <IconDownload size={14} stroke={2} /> Unduh semuanya (JSON)
      </button>
    </div>
  )
}
