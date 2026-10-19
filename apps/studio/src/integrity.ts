/**
 * What a tier collects, and under which consent. Mirrors
 * services/core/internal/domain/integrity.go: the server is the one that
 * enforces it, this side only avoids collecting what would be dropped anyway.
 */

export type Purpose = 'signals' | 'fullscreen' | 'camera'

/** What a tier needs the candidate to agree to before the attempt starts. Tier 0 collects nothing. */
export function requiredConsents(tier: number): Purpose[] {
  if (tier <= 0) return []
  if (tier === 1) return ['signals']
  return ['signals', 'fullscreen', 'camera']
}

const KINDS: Record<string, Purpose> = {
  'focus.lost': 'signals',
  'focus.returned': 'signals',
  'answer.pasted': 'signals',
  'item.viewed': 'signals',
  'device.seen': 'signals',
  'session.second_tab': 'signals',
  'fullscreen.exited': 'fullscreen',
  'vision.face_missing': 'camera',
  'vision.faces_multiple': 'camera',
  'vision.stopped': 'camera',
}

/** The consent an event is collected under, or undefined for answers and the like. */
export const purposeOf = (kind: string): Purpose | undefined => KINDS[kind]

/** May this tier collect this event at all? */
export const collects = (tier: number, kind: string) => {
  const p = purposeOf(kind)
  return !p || requiredConsents(tier).includes(p)
}

/** What each purpose means, in the candidate's words (UU PDP No. 27/2022: specific, separate, withdrawable). */
export const PURPOSES: Record<Purpose, { title: string; what: string; keep: string }> = {
  signals: {
    title: 'Catatan aktivitas pengerjaan',
    what:
      'Kapan Anda meninggalkan halaman ujian dan berapa lama, kapan teks ditempel ke jawaban (jumlah karakternya saja), ' +
      'berapa lama Anda di tiap soal, jenis perangkat, dan tab kedua yang ditolak.',
    keep: 'Disimpan bersama jawaban Anda paling lama 1 tahun setelah ujian, lalu dihapus. Isi teks yang ditempel dan isi tab lain tidak pernah dicatat.',
  },
  fullscreen: {
    title: 'Layar penuh',
    what: 'Ujian dibuka di layar penuh, dan setiap kali Anda keluar dari layar penuh dicatat.',
    keep: 'Hanya waktu keluarnya yang dicatat, disimpan paling lama 1 tahun setelah ujian.',
  },
  camera: {
    title: 'Pemeriksaan wajah oleh perangkat',
    what:
      'Kamera depan diperiksa di perangkat Anda sendiri, sekitar sekali per detik: apakah ada wajah, dan apakah ada lebih dari satu. ' +
      'Video dan foto tidak pernah dikirim atau disimpan. Anda dapat mematikan kamera kapan saja selama ujian; ' +
      'itu dicatat dan ditinjau penyelenggara.',
    keep: 'Hanya peristiwanya yang dikirim, misalnya "tidak ada wajah selama 40 detik", dan disimpan paling lama 1 tahun setelah ujian.',
  },
}
