/**
 * Verify an Open Badges 3.0 credential issued by this deployment, in the
 * browser: fetch it by id, fetch its issuer's public keys, check the RS256
 * signature with WebCrypto, and check it has not been revoked. Optionally,
 * check it was issued to an email address the viewer already knows; the
 * credential holds only a salted hash of it.
 */

type Jwk = JsonWebKey & { kid?: string }
type Badge = {
  iss: string
  jti: string
  validFrom?: string
  name?: string
  issuer?: { id: string; name?: string }
  credentialSubject?: {
    identifier?: { identityHash?: string; salt?: string; identityType?: string }[]
    achievement?: { name?: string; description?: string; criteria?: { narrative?: string } }
  }
}
type Verdict =
  | { ok: true; vc: Badge; revoked: false; jwt: string }
  | { ok: false; why: string; vc?: Badge; jwt?: string }

const out = document.getElementById('out')!

const b64 = (s: string) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0))
const text = (s: string) => new TextDecoder().decode(b64(s))

async function verify(id: string): Promise<Verdict> {
  if (!/^[0-9a-f-]{36}$/i.test(id)) return { ok: false, why: 'Tautan ini tidak memuat nomor sertifikat yang sah.' }
  const res = await fetch(`/credentials/${id}`)
  if (res.status === 404) return { ok: false, why: 'Sertifikat dengan nomor ini tidak ada.' }
  if (!res.ok) return { ok: false, why: 'Layanan pemeriksaan sedang tidak tersedia. Coba lagi nanti.' }
  const { jwt, revoked, revokedReason } = (await res.json()) as { jwt: string; revoked: boolean; revokedReason?: string }
  const [h, p, sig] = jwt.split('.')
  if (!h || !p || !sig) return { ok: false, why: 'Sertifikat ini rusak.' }
  const head = JSON.parse(text(h)) as { alg: string; kid: string }
  const vc = JSON.parse(text(p)) as Badge
  // The issuer must be this deployment: its keys are the trust anchor here.
  const iss = new URL(vc.iss)
  if (iss.origin !== location.origin || !iss.pathname.startsWith('/issuers/') || head.alg !== 'RS256')
    return { ok: false, why: 'Sertifikat ini tidak diterbitkan di sini.', vc, jwt }
  if (vc.jti !== `urn:uuid:${id}`) return { ok: false, why: 'Isi sertifikat tidak cocok dengan nomornya.', vc, jwt }
  const { keys } = (await (await fetch(`${iss.pathname}/jwks`)).json()) as { keys: Jwk[] }
  const jwk = keys.find((k) => k.kid === head.kid)
  if (!jwk) return { ok: false, why: 'Kunci penerbit untuk sertifikat ini tidak ditemukan.', vc, jwt }
  const key = await crypto.subtle.importKey('jwk', jwk, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, false, ['verify'])
  const good = await crypto.subtle.verify('RSASSA-PKCS1-v1_5', key, b64(sig), new TextEncoder().encode(`${h}.${p}`))
  if (!good) return { ok: false, why: 'Tanda tangan sertifikat ini tidak sah: isinya telah diubah.', vc, jwt }
  if (revoked)
    return { ok: false, why: `Sertifikat ini telah dicabut${revokedReason ? `: ${translate(revokedReason)}` : ''}.`, vc, jwt }
  return { ok: true, vc, revoked: false, jwt }
}

function translate(reason: string) {
  return reason === 'a later review of the attempt brought the score below the pass mark'
    ? 'penilaian ulang menurunkan nilainya di bawah ambang lulus'
    : reason
}

async function sha256Hex(s: string) {
  const d = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(s))
  return Array.from(new Uint8Array(d), (b) => b.toString(16).padStart(2, '0')).join('')
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Record<string, string> = {}, ...kids: (Node | string)[]) {
  const e = document.createElement(tag)
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v)
  e.append(...kids)
  return e
}

function render(v: Verdict) {
  out.replaceChildren()
  out.append(
    el('div', { class: `verdict ${v.ok ? 'ok' : 'bad'}`, role: 'status' }, v.ok ? '✓ Sertifikat sah' : '✕ Tidak sah'),
  )
  if (!v.ok) out.append(el('p', {}, v.why))
  const vc = v.vc
  if (!vc) return
  const a = vc.credentialSubject?.achievement
  out.append(el('p', { class: 'title' }, a?.name ?? vc.name ?? ''))
  if (a?.description) out.append(el('p', { class: 'sub' }, a.description))
  const dl = el('dl')
  const row = (k: string, val?: string) => val && dl.append(el('dt', {}, k), el('dd', {}, val))
  row('Penerbit', vc.issuer?.name)
  row('Diterbitkan', vc.validFrom ? new Date(vc.validFrom).toLocaleDateString('id-ID', { dateStyle: 'long' }) : undefined)
  row('Kriteria', a?.criteria?.narrative)
  row('Nomor', vc.jti.replace('urn:uuid:', ''))
  out.append(dl)

  const ident = vc.credentialSubject?.identifier?.find((i) => i.identityType === 'emailAddress')
  if (v.ok && ident?.identityHash && ident.salt) {
    const input = el('input', { id: 'email', type: 'email', autocomplete: 'off', placeholder: 'nama@contoh.id', required: '' }) as HTMLInputElement
    const note = el('p', { class: 'note', role: 'status' })
    const form = el(
      'form',
      {},
      el('label', { for: 'email' }, 'Untuk siapa? Masukkan email yang Anda ketahui untuk mencocokkan. Email tidak dikirim ke mana pun.'),
      el('div', { class: 'row' }, input, el('button', { type: 'submit' }, 'Cocokkan')),
      note,
    )
    form.addEventListener('submit', async (e) => {
      e.preventDefault()
      const h = await sha256Hex(input.value.trim().toLowerCase() + ident.salt)
      const match = `sha256$${h}` === ident.identityHash
      note.textContent = match ? '✓ Sertifikat ini diterbitkan untuk email tersebut.' : '✕ Sertifikat ini tidak diterbitkan untuk email tersebut.'
      note.style.color = match ? 'var(--good)' : 'var(--bad)'
    })
    out.append(form)
  }
  if (v.jwt) {
    const blob = URL.createObjectURL(new Blob([v.jwt], { type: 'application/jwt' }))
    out.append(
      el('p', { class: 'foot' },
        'Diperiksa di peramban Anda dengan kunci publik penerbit. ',
        el('a', { href: blob, download: `sertifikat-${vc.jti.replace('urn:uuid:', '')}.jwt` }, 'Unduh sertifikat (VC-JWT)'),
        ' untuk dompet digital atau pemeriksa Open Badges lain.'),
    )
  }
}

const id = decodeURIComponent(location.hash.slice(1) || new URLSearchParams(location.search).get('id') || '')
verify(id).then(render, () => render({ ok: false, why: 'Pemeriksaan gagal. Periksa koneksi Anda lalu coba lagi.' }))
