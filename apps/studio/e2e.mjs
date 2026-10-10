/**
 * Smoke test for the loop that must never break:
 * author → publish → invite (one by one, CSV, Excel) → open the emailed link → verify the
 * code → sit it → be scored → resume after a reload,
 * plus the ungraded questionnaire path and its aggregate.
 */
import { chromium } from 'playwright'

const URL = process.env.URL ?? 'http://localhost:5180'
const fails = []
const ok = (m) => console.log(`  \x1b[32m✓\x1b[0m ${m}`)
const bad = (m) => { fails.push(m); console.log(`  \x1b[31m✗\x1b[0m ${m}`) }

// CHROMIUM_PATH reuses an installed Chromium instead of Playwright's own download.
const browser = await chromium.launch(
  process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {},
)
// One context: a second tab shares the store, the way a second device would share a server.
const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await ctx.newPage()
const errors = []
page.on('console', (c) => c.type() === 'error' && errors.push(c.text()))
page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message))

const nav = async (name) => {
  const exit = page.getByRole('button', { name: /^Exit$/ })
  if (await exit.count()) { await exit.click(); await page.waitForTimeout(250) }
  await page.getByRole('button', { name }).first().click()
  await page.waitForTimeout(400)
}

/** A one-sheet .xlsx, stored uncompressed — the smallest workbook Excel itself would open. */
const xlsx = (rows) => {
  const esc = (t) => t.replace(/&/g, '&amp;').replace(/</g, '&lt;')
  const col = (i) => String.fromCharCode(65 + i)
  const sheet =
    '<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>' +
    rows
      .map((r, y) => `<row r="${y + 1}">${r.map((v, x) => `<c r="${col(x)}${y + 1}" t="inlineStr"><is><t>${esc(v)}</t></is></c>`).join('')}</row>`)
      .join('') +
    '</sheetData></worksheet>'
  const files = [['xl/worksheets/sheet1.xml', Buffer.from(sheet)]]
  const crc = (buf) => {
    let c = ~0
    for (const b of buf) { c ^= b; for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1)) }
    return ~c >>> 0
  }
  const locals = []
  const central = []
  let offset = 0
  for (const [name, data] of files) {
    const n = Buffer.from(name)
    const head = Buffer.alloc(30)
    head.writeUInt32LE(0x04034b50, 0); head.writeUInt16LE(20, 4); head.writeUInt32LE(crc(data), 14)
    head.writeUInt32LE(data.length, 18); head.writeUInt32LE(data.length, 22); head.writeUInt16LE(n.length, 26)
    const dir = Buffer.alloc(46)
    dir.writeUInt32LE(0x02014b50, 0); dir.writeUInt16LE(20, 4); dir.writeUInt16LE(20, 6); dir.writeUInt32LE(crc(data), 16)
    dir.writeUInt32LE(data.length, 20); dir.writeUInt32LE(data.length, 24); dir.writeUInt16LE(n.length, 28); dir.writeUInt32LE(offset, 42)
    locals.push(head, n, data)
    central.push(dir, n)
    offset += 30 + n.length + data.length
  }
  const cd = Buffer.concat(central)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(files.length, 8); end.writeUInt16LE(files.length, 10)
  end.writeUInt32LE(cd.length, 12); end.writeUInt32LE(offset, 16)
  return Buffer.concat([...locals, cd, end])
}

/** The candidate picker keeps open-access instruments behind a demo disclosure. */
const openDemoList = async () => {
  const sum = page.getByText('Demo — buka tanpa undangan')
  if (await sum.count()) await sum.click()
  await page.waitForTimeout(250)
}

/** Answer whatever control this question offers. */
const answerHere = async (text = 'lag') => {
  const choices = page.locator('main button[aria-pressed]')
  if (await choices.count()) { await choices.first().click(); return true }
  const box = page.getByPlaceholder(/Jawaban Anda|Tulis jawaban/).first()
  if (await box.count()) { await box.fill(text); return true }
  return false
}

try {
  await page.goto(URL, { waitUntil: 'networkidle' })

  /* ---------------- library ---------------- */
  const listed = await page.getByText('Sertifikasi Backend Engineer — Level 3').count()
  const listedSurvey = await page.getByText('Kuesioner Pengalaman Onboarding').count()
  listed > 0 && listedSurvey > 0
    ? ok('library lists every instrument on landing')
    : bad(`library did not list both instruments (exam=${listed}, survey=${listedSurvey})`)

  await page.getByRole('button', { name: /^Questionnaires/ }).click()
  await page.waitForTimeout(300)
  ;(await page.getByText('Sertifikasi Backend Engineer — Level 3').count()) === 0
    ? ok('type filter narrows the library')
    : bad('filter did not exclude the exam')
  await page.getByRole('button', { name: /^All/ }).click()
  await page.waitForTimeout(250)

  await page.getByPlaceholder('Search by title or issuer').fill('Kuesioner')
  await page.waitForTimeout(300)
  ;(await page.getByText('Sertifikasi Backend Engineer — Level 3').count()) === 0
    ? ok('search narrows the library')
    : bad('search did not filter')
  await page.getByPlaceholder('Search by title or issuer').fill('')
  await page.waitForTimeout(250)

  /* ---------------- author ---------------- */
  await page.getByRole('button', { name: /Sertifikasi Backend Engineer/ }).first().click()
  await page.waitForTimeout(400)
  ;(await page.getByLabel('Exam title').count())
    ? ok('opening from the library lands in the editor on that instrument')
    : bad('opening from the library did not reach the editor')
  await page.getByLabel('Exam title').fill('E2E — Rekrutmen Backend')
  await page.getByRole('button', { name: /^Add question$/ }).first().click()
  await page.waitForTimeout(250)
  await page.getByLabel('Question', { exact: true }).fill('Apa keluaran dari len(nil slice) di Go?')
  for (const [i, t] of ['0', 'panic', '-1', 'undefined'].entries())
    await page.getByPlaceholder(`Option ${'ABCD'[i]}`).fill(t)
  await page.getByRole('button', { name: /^Correct$/ }).first().click()
  ok('authored a question with options and a correct answer')

  // real oversized PNG, so the downscale path is exercised rather than bypassed
  await page.evaluate(async (sel) => {
    const c = document.createElement('canvas')
    c.width = 2000; c.height = 1200
    const x = c.getContext('2d')
    x.fillStyle = '#2a78d6'; x.fillRect(0, 0, 2000, 1200)
    x.fillStyle = '#fff'; x.font = 'bold 160px sans-serif'; x.fillText('DIAGRAM', 180, 660)
    const blob = await new Promise((r) => c.toBlob(r, 'image/png'))
    const dt = new DataTransfer()
    dt.items.add(new File([blob], 'diagram.png', { type: 'image/png' }))
    const el = document.querySelector(sel)
    el.files = dt.files
    el.dispatchEvent(new Event('change', { bubbles: true }))
  }, 'input[type="file"][aria-label="Add a figure to the question"]')
  await page.waitForTimeout(700)
  ;(await page.getByText(/Needed for screen readers/).count())
    ? ok('image attached, and missing alt text is flagged')
    : bad('image did not attach, or alt text was not demanded')

  await page.getByLabel('Image description (alt text)').fill('Diagram alur permintaan ke channel audit')
  await page.waitForTimeout(300)
  ;(await page.getByText(/missing alt text/).count()) === 0
    ? ok('alt-text warning clears once described')
    : bad('alt-text warning stuck after filling it in')

  const meta = await page.getByText(/\d+×\d+ · \d+ KB/).first().innerText().catch(() => '')
  const w = Number(meta.match(/^(\d+)×/)?.[1] ?? 0)
  w > 0 && w <= 1400
    ? ok(`downscaled on ingest: ${meta} (source was 2000×1200)`)
    : bad(`expected downscale to <=1400px, got "${meta}"`)

  await page.getByRole('button', { name: /Apa keluaran dari len/ }).first().click()
  await page.waitForTimeout(250)
  ;(await page.getByText('Apa keluaran dari len(nil slice) di Go?').count())
    ? ok('collapsed card still shows the question')
    : bad('question text lost on collapse')

  await page.getByRole('button', { name: /Save changes/ }).click()
  await page.waitForTimeout(400)
  ;(await page.getByText(/is the version candidates are sitting/).count())
    ? ok('editing a live instrument asks before overwriting it')
    : bad('no version prompt on saving a live instrument')

  await page.getByRole('button', { name: /Save as v2/ }).click()
  await page.waitForTimeout(450)
  ;(await page.getByText(/v1 live · editing v2/).count())
    ? ok('branching leaves v1 live and moves the working copy to v2')
    : bad('branch did not create a separate working version')

  await page.getByRole('button', { name: /Publish v2/ }).click()
  await page.waitForTimeout(450)
  ;(await page.getByText(/published v2/i).count())
    ? ok('publishing v2 makes it the version candidates get')
    : bad('v2 did not go live')

  /* ---------------- lifecycle: draft → published → archived ---------------- */
  await nav(/Library/)
  await page.getByRole('button', { name: /^New$/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /^Quiz/ }).click()
  await page.waitForTimeout(450)
  await page.getByLabel('Exam title').fill('E2E Draft Quiz')
  ;(await page.getByRole('button', { name: /^Publish v1$/ }).count())
    ? ok('new instruments start as drafts at v1, with publish as a separate action')
    : bad('a new instrument was not a v1 draft')
  await page.getByRole('button', { name: /Save draft/ }).click()
  await page.waitForTimeout(350)

  await nav(/Take exam/i)
  await openDemoList()
  ;(await page.getByRole('button', { name: /E2E Draft Quiz/ }).count()) === 0
    ? ok('a draft is not offered to candidates')
    : bad('a draft was sittable')

  await nav(/Library/)
  await page.getByRole('button', { name: /Actions for E2E Draft Quiz/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /^Publish$/ }).click()
  await page.waitForTimeout(400)
  await nav(/Take exam/i)
  await openDemoList()
  ;(await page.getByRole('button', { name: /E2E Draft Quiz/ }).count())
    ? ok('publishing makes it available to candidates')
    : bad('publishing did not expose it')

  await nav(/Library/)
  await page.getByRole('button', { name: /Actions for E2E Draft Quiz/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /^Archive$/ }).click()
  await page.waitForTimeout(400)
  ;(await page.getByText('E2E Draft Quiz').count()) === 0
    ? ok('archiving removes it from the active list')
    : bad('archived instrument still listed as active')
  await page.getByRole('button', { name: /archived/i }).first().click()
  await page.waitForTimeout(350)
  ;(await page.getByText('E2E Draft Quiz').count())
    ? ok('archived instruments are recoverable, not destroyed')
    : bad('archived instrument vanished entirely')

  /* ---------------- roster and assignment ---------------- */
  await nav(/People/)
  ;(await page.getByText('budi.santoso@example.id').count())
    ? ok('roster screen lists people')
    : bad('roster was empty')

  await page.getByRole('button', { name: /^Import$/ }).click()
  await page.waitForTimeout(300)
  await page.getByPlaceholder(/Budi Santoso, budi@example.id/).fill(
    'Dewi Lestari, dewi.lestari@example.id, QA\nbudi.santoso@example.id',
  )
  await page.getByRole('button', { name: /Import roster/ }).click()
  await page.waitForTimeout(400)
  ;(await page.getByText(/Added 1.*skipped 1/).count())
    ? ok('roster import adds new people and skips duplicate emails')
    : bad('import did not report add/skip correctly')

  await nav(/Library/)
  await page.getByRole('button', { name: /Actions for E2E — Rekrutmen Backend/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /Assign people/ }).click()
  await page.waitForTimeout(450)
  await page.getByRole('button', { name: /^Select Budi Santoso$/ }).click()
  await page.waitForTimeout(150)
  await page.getByRole('button', { name: /^Invite 1 person$/ }).click()
  await page.waitForTimeout(600)
  const budiLink = page.getByRole('link', { name: 'Open the link emailed to budi.santoso@example.id' })
  ;(await budiLink.count())
    ? ok('inviting a person emails them their own link')
    : bad('no invitation email in the outbox')
  ;(await page.getByRole('button', { name: /Copy invitation/ }).count()) === 0
    ? ok('there is no way to copy a link back out of an assignment')
    : bad('a link can still be copied from the assignment')
  ;(await page.getByText(/^v2$/).count())
    ? ok('an invitation pins the version it was issued against')
    : bad('invitation did not record a version')

  const budiHref = await budiLink.first().getAttribute('href')
  const budiToken = budiHref.split('#/i/')[1]
  const stored = await page.evaluate(() =>
    Object.keys(localStorage).filter((k) => k !== 'am.outbox.v1').map((k) => localStorage.getItem(k)).join('\n'),
  )
  budiToken?.length === 43 && !stored.includes(budiToken)
    ? ok('the link is 256 bits and only its hash is stored')
    : bad('the raw link token is stored, or is not 256 bits')

  // invite an address that was never on the roster
  await page.getByRole('button', { name: /Invite someone not on the roster/ }).click()
  await page.waitForTimeout(300)
  await page.getByPlaceholder(/Agus Wibowo/).fill('Agus Wibowo <agus.wibowo@example.id>')
  await page.getByRole('button', { name: /Invite by email/ }).click()
  await page.waitForTimeout(450)
  ;(await page.getByText(/Invited 1, 1 added to the roster/).count())
    ? ok('off-roster addresses can be invited, and join the roster')
    : bad('email invite did not add and assign')
  ;(await page.getByText('agus.wibowo@example.id').count())
    ? ok('the ad-hoc invitee appears in the assignment table')
    : bad('ad-hoc invitee missing')

  // bulk, from a CSV: header in any order, an extra column that must not be kept, a bad row
  await page.getByLabel('Upload a CSV or Excel roster').setInputFiles({
    name: 'kandidat.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(
      'NIK;Email;Nama;Kelompok\n3174000000000001;sari.wulandari@example.id;Sari Wulandari;QA\n3174000000000002;bukan-email;Tono;QA\n3174000000000003;eko.prasetyo@example.id;Eko Prasetyo;QA\n',
    ),
  })
  await page.waitForTimeout(400)
  const csvReport = page.getByTestId('roster-report')
  ;(await csvReport.getByText('2 new').count()) && (await csvReport.getByText(/1 rows cannot be used/).count())
    ? ok('a CSV is previewed before anything is written: 2 new, 1 unusable row')
    : bad('CSV preview did not count new and rejected rows')
  await page.getByRole('button', { name: /^Invite 2 people$/ }).click()
  await page.waitForTimeout(600)
  ;(await page.getByText(/Invited 2, 2 added to the roster/).count()) &&
  (await page.getByRole('link', { name: 'Open the link emailed to eko.prasetyo@example.id' }).count())
    ? ok('a CSV invites everyone usable in it, and each gets an email')
    : bad('CSV import did not invite and email')
  ;(await page.evaluate(() => JSON.stringify(localStorage))).includes('3174000000000001')
    ? bad('a column the importer does not use was stored')
    : ok('columns other than name, email and group are dropped, not stored')

  // bulk, from an Excel workbook
  await page.getByLabel('Upload a CSV or Excel roster').setInputFiles({
    name: 'kandidat.xlsx',
    mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    buffer: xlsx([
      ['Full name', 'E-mail', 'Team'],
      ['Rina Kusuma', 'rina.kusuma@example.id', 'Data'],
    ]),
  })
  await page.waitForTimeout(400)
  await page.getByRole('button', { name: /^Invite 1 person$/ }).last().click()
  await page.waitForTimeout(600)
  ;(await page.getByRole('link', { name: 'Open the link emailed to rina.kusuma@example.id' }).count())
    ? ok('an .xlsx roster is read and its people invited')
    : bad('.xlsx import did not invite')

  /* ---------------- the link, from the other side ---------------- */
  const other = await ctx.newPage()
  const holderSees = async (href) => {
    await other.goto(href)
    await other.waitForTimeout(700)
    return other.locator('main').innerText()
  }

  ;(await holderSees(`${URL}/#/i/${'A'.repeat(43)}`)).includes('Tautan ini tidak dikenali')
    ? ok('a made-up link is refused without saying anything about anyone')
    : bad('a made-up link was not refused')

  // revoke, then resend
  const agusHref = await page.getByRole('link', { name: 'Open the link emailed to agus.wibowo@example.id' }).getAttribute('href')
  await page.getByRole('button', { name: 'Revoke invitation for Agus Wibowo' }).click()
  await page.waitForTimeout(400)
  ;(await holderSees(agusHref)).includes('Undangan ini telah ditarik')
    ? ok('a revoked link stops working')
    : bad('a revoked link still opened')
  await page.getByRole('button', { name: 'Resend invitation to Agus Wibowo' }).click()
  await page.waitForTimeout(500)
  ;(await holderSees(agusHref)).includes('Tautan ini sudah diganti')
    ? ok('resending retires the previous link')
    : bad('the previous link survived a resend')

  /* ---------------- sit it through the invitation ---------------- */
  await budiLink.first().click()
  await page.waitForTimeout(700)
  const gateText = await page.locator('main').innerText()
  gateText.includes('Pastikan ini memang Anda') && gateText.includes('b•••@example.id') && !gateText.includes('budi.santoso')
    ? ok('opening a link asks for a code and shows only a masked address')
    : bad('the link opened without asking for a code, or showed the full address')
  ;(await page.evaluate(() => location.hash)).includes(budiToken)
    ? bad('the token stayed in the address bar')
    : ok('the token is taken out of the address bar on arrival')

  await page.getByRole('button', { name: /Kirim kode/ }).click()
  await page.waitForTimeout(500)
  const code = await page.getByTestId('stub-code').innerText()
  await page.getByLabel('Kode verifikasi').fill(code === '000000' ? '111111' : '000000')
  await page.getByRole('button', { name: /^Verifikasi/ }).click()
  await page.waitForTimeout(400)
  ;(await page.getByText(/Kode tidak cocok. Sisa 4 dari 5/).count())
    ? ok('a wrong code costs a try and says how many are left')
    : bad('a wrong code was not counted')
  await page.getByLabel('Kode verifikasi').fill(code)
  await page.getByRole('button', { name: /^Verifikasi/ }).click()
  await page.waitForTimeout(600)
  ;(await page.getByText(/Untuk Budi Santoso/).count()) && (await page.getByText('email terverifikasi').count())
    ? ok('the right code opens the invitation for that person')
    : bad('verifying the code did not open the invitation')

  ;(await holderSees(budiHref)).includes('Tautan ini sudah dipakai')
    ? ok('a link is spent once its code is verified')
    : bad('a spent link opened again')
  await other.close()

  ;(await page.getByText(/Yang kami kumpulkan/).count())
    ? ok('invitation states what will be collected before starting')
    : bad('invitation did not disclose data collection')

  await page.getByRole('button', { name: /^Mulai$/ }).click()
  await page.waitForTimeout(300)
  ;(await page.getByText(/UU PDP No. 27\/2022/).count())
    ? ok('Tier 3 asks for per-purpose consent')
    : bad('consent stage was skipped on a Tier 3 exam')
  await page.getByRole('button', { name: /Setuju dan lanjut/ }).click()
  await page.waitForTimeout(300)
  await page.getByRole('button', { name: /Mulai sekarang/ }).click()
  await page.waitForTimeout(450)

  // the browser dies after one answer; the session the link started brings them back
  await answerHere()
  await page.waitForTimeout(300)
  await page.reload({ waitUntil: 'networkidle' })
  await page.waitForTimeout(700)
  ;(await page.getByText(/Anda punya pengerjaan yang tertunda/).count())
    ? ok('a reload mid-exam comes back through the session, with the work kept')
    : bad('a reload mid-exam lost the session or the work')
  await page.getByRole('button', { name: /Lanjutkan/ }).click()
  await page.waitForTimeout(400)
  const total = Number((await page.getByText(/Pertanyaan 1 dari \d+/i).first().innerText()).match(/dari (\d+)/i)[1])
  ok(`attempt started with ${total} questions`)

  let sawFigure = 0
  for (let i = 0; i < total; i++) {
    sawFigure += await page.locator('main img[alt*="Diagram alur"]').count()
    // The first was answered before the reload.
    if (i > 0 && !(await answerHere())) bad(`question ${i + 1} had no answer control`)
    await page.waitForTimeout(110)
    if (i < total - 1) await page.getByRole('button', { name: /^Berikutnya/ }).click()
    await page.waitForTimeout(110)
  }
  ok('answered every question')
  sawFigure > 0 ? ok('figure rendered for the participant') : bad('figure never appeared')

  await page.getByRole('button', { name: /^Selesai/ }).click()
  await page.waitForTimeout(350)
  ;(await page.getByText(/Tinjau sebelum mengumpulkan/).count())
    ? ok('review step lists answered / flagged / unanswered')
    : bad('no review step before submit')

  await page.getByRole('button', { name: /^Kumpulkan$/ }).click()
  await page.waitForTimeout(300)
  ;(await page.getByText(/tidak dapat mengubah jawaban/).count())
    ? ok('submit asks for confirmation and warns it is final')
    : bad('submitted without confirmation')
  await page.getByRole('button', { name: /Ya, kumpulkan/ }).click()
  await page.waitForTimeout(600)

  const pct = await page.getByText(/^\d+%$/).first().innerText()
  const verdict = await page.getByText(/Lulus|Belum lulus/).first().innerText()
  ok(`scored ${pct} · ${verdict}`)

  await page.getByRole('button', { name: /Apa yang kami catat/ }).click()
  await page.waitForTimeout(300)
  const ev = await page.getByText(/\d+ peristiwa/).first().innerText()
  const n = Number(ev.match(/(\d+) peristiwa/)[1])
  n >= total + 2
    ? ok(`participant can see what was recorded — ${ev}`)
    : bad(`only ${ev}, expected >= ${total + 2}`)

  await nav(/Assignments/)
  await page.waitForTimeout(500)
  ;(await page.getByText('Submitted').count())
    ? ok('completing an invitation updates the assignment to submitted')
    : bad('assignment status did not follow the attempt')
  ;(await page.getByText(/^100%$/).count())
    ? ok('the score lands back on the roster row')
    : bad('score did not reach the assignment')

  /* ---------------- questionnaire, opened straight from the library ---------------- */
  await nav(/Library/)
  await page.getByRole('button', { name: /Preview Kuesioner Pengalaman Onboarding/ }).click()
  await page.waitForTimeout(500)
  ;(await page.getByText('Kuesioner Pengalaman Onboarding').count())
    ? ok('“preview as candidate” opens that instrument, skipping the picker')
    : bad('preview as candidate did not open the questionnaire')
  ;(await page.getByText(/Ambang lulus/).count()) === 0
    ? ok('questionnaire shows no pass threshold')
    : bad('pass threshold leaked into an ungraded instrument')

  await page.getByRole('button', { name: /Mulai mengisi/ }).click()
  await page.waitForTimeout(300)
  ;(await page.getByText(/UU PDP No. 27\/2022/).count()) === 0
    ? ok('untiered questionnaire skips the consent gate')
    : bad('consent gate appeared on a Tier 0 questionnaire')
  await page.getByRole('button', { name: /Mulai sekarang/ }).click()
  await page.waitForTimeout(400)

  const qTotal = Number((await page.getByText(/Pertanyaan 1 dari \d+/i).first().innerText()).match(/dari (\d+)/i)[1])

  // required questions must block a submit
  await page.getByRole('button', { name: /Tinjau/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('button', { name: /Kirim jawaban/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('button', { name: /Ya, kumpulkan/ }).click()
  await page.waitForTimeout(400)
  ;(await page.getByText(/wajib dijawab/).count())
    ? ok('submit refused while required questions are blank, and jumps to the first')
    : bad('required questions were not enforced')

  for (let i = 0; i < qTotal; i++) {
    await answerHere('Beri akses perangkat lebih awal.')
    await page.waitForTimeout(110)
    if (i < qTotal - 1) await page.getByRole('button', { name: /^Berikutnya/ }).click()
    await page.waitForTimeout(110)
  }
  await page.getByRole('button', { name: /^Selesai/ }).click()
  await page.waitForTimeout(300)
  await page.getByRole('button', { name: /Kirim jawaban/ }).click()
  await page.waitForTimeout(250)
  await page.getByRole('button', { name: /Ya, kumpulkan/ }).click()
  await page.waitForTimeout(600)
  ;(await page.getByText(/Terima kasih/).count())
    ? ok('questionnaire submitted — thanked, not graded')
    : bad('questionnaire produced a grade instead of a thank-you')

  await nav(/Aggregate results/)
  const mean = await page.getByText(/mean \d/).first().count()
  const answered = await page.getByText(/1 of 1 answered/).first().count()
  mean > 0 && answered > 0
    ? ok('aggregate shows distributions + mean, and counts ONE response per submit')
    : bad(`aggregate did not render (mean=${mean}, answered=${answered})`)

  /* ── QTI 3.0: our own package round-trips; a 2.1 item from elsewhere reads ── */
  const qti = await page.evaluate(async () => {
    const { exportQti, importQti } = await import('/src/qti.ts')
    const exams = JSON.parse(localStorage.getItem('am.exams.v1') ?? '[]')
    // A figure on a stem and on an option, so the images travel too.
    const c = document.createElement('canvas')
    c.width = 6; c.height = 4
    const g = c.getContext('2d'); g.fillStyle = '#c33'; g.fillRect(0, 0, 3, 4)
    const pic = { id: 'm_t', url: c.toDataURL('image/png'), alt: 'red half', w: 6, h: 4, bytes: 80 }
    const out = []
    for (const original of exams) {
      const e = structuredClone(original)
      const choice = e.sections.flatMap((s) => s.items).find((i) => i.choices.length && i.type !== 'boolean')
      if (choice) { choice.image = { ...pic }; choice.choices[0].image = { ...pic, alt: 'opt' } }
      e.sections[0].items[0].stem += '\n\n  two spaces & <angle> "quotes"'
      const blob = await exportQti(e)
      const { exam: back, warnings } = await importQti(new File([blob], 'x.qti.zip'))
      const canon = (x) => {
        const strip = (m) => (m ? { alt: m.alt, data: m.url.startsWith('data:image/') } : undefined)
        const y = structuredClone(x)
        for (const k of ['id', 'status', 'version', 'publishedVersion', 'updatedAt']) delete y[k]
        for (const s of y.sections) for (const i of s.items) {
          i.image = strip(i.image)
          for (const ch of i.choices) ch.image = strip(ch.image)
        }
        const sort = (v) => Array.isArray(v) ? v.map(sort) : v && typeof v === 'object'
          ? Object.fromEntries(Object.keys(v).filter((k) => v[k] !== undefined).sort().map((k) => [k, sort(v[k])])) : v
        return JSON.stringify(sort(y))
      }
      out.push({ title: e.title, same: canon(e) === canon(back), warnings, a: canon(e), b: canon(back) })
    }
    const foreign = `<?xml version="1.0"?>
      <assessmentItem xmlns="http://www.imsglobal.org/xsd/imsqti_v2p1" identifier="2plus2" title="Sum">
        <responseDeclaration identifier="RESPONSE" cardinality="single" baseType="identifier">
          <correctResponse><value>B</value></correctResponse>
        </responseDeclaration>
        <outcomeDeclaration identifier="SCORE" cardinality="single" baseType="float"/>
        <itemBody>
          <p>What is
             2 + 2?</p>
          <choiceInteraction responseIdentifier="RESPONSE" shuffle="false" maxChoices="1">
            <simpleChoice identifier="A">3</simpleChoice>
            <simpleChoice identifier="B">4</simpleChoice>
          </choiceInteraction>
          <img src="http://evil.example/x.png" alt=""/>
        </itemBody>
      </assessmentItem>`
    const { writeZip } = await import('/src/zip.ts')
    const zip = writeZip([['item1.xml', new TextEncoder().encode(foreign)]])
    const f = (await importQti(new File([zip], 'bank.zip'))).exam
    const it = f.sections[0]?.items[0]
    return {
      out,
      foreign: it && it.stem === 'What is 2 + 2?' && it.type === 'single' && it.id === '_2plus2'.slice(1) &&
        it.choices.map((c) => c.text).join() === '3,4' && it.correct.join() === 'B' && !it.image && f.kind === 'exam',
      foreignDump: JSON.stringify(f).slice(0, 600),
    }
  })
  for (const r of qti.out)
    r.same && r.warnings.length === 0
      ? ok(`QTI round-trip is lossless: "${r.title}"`)
      : bad(`QTI round-trip lost something in "${r.title}" (warnings ${JSON.stringify(r.warnings)})\n    ${r.a}\n    ${r.b}`)
  qti.foreign ? ok('a QTI 2.1 item from another tool imports; an outside image is dropped') : bad(`QTI 2.1 import: ${qti.foreignDump}`)

  // The same through the screens: export from the library, import the file, land in the editor.
  await nav(/Library/)
  const first = page.getByRole('button', { name: /^Actions for / }).first()
  await first.click()
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('menuitem', { name: /Export QTI/ }).click()])
  const path = await download.path()
  const before = (await page.evaluate(() => JSON.parse(localStorage.getItem('am.exams.v1')).length))
  await page.locator('input[aria-label="QTI package"]').setInputFiles(path)
  await page.waitForTimeout(800)
  const after = (await page.evaluate(() => JSON.parse(localStorage.getItem('am.exams.v1')).length))
  after === before + 1 && download.suggestedFilename().endsWith('.qti.zip')
    ? ok('Export QTI downloads a package and Import QTI makes it a new draft')
    : bad(`QTI via the screens: ${download.suggestedFilename()} ${before} → ${after}`)
} catch (e) {
  bad(`threw: ${String(e.message).split('\n').slice(0, 6).join(' | ')}`)
} finally {
  if (errors.length) bad(`console/page errors: ${JSON.stringify(errors.slice(0, 3))}`)
  else ok('no console or page errors')
  await browser.close()
}

if (fails.length) {
  console.log(`\n\x1b[31m${fails.length} check(s) failed\x1b[0m`)
  process.exit(1)
}
console.log('\n\x1b[32mall checks passed\x1b[0m')
