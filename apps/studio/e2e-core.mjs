/**
 * The same loop as e2e.mjs, against the real Go core and Postgres:
 * sign in by emailed code → author → publish → add someone → invite → open
 * the emailed link in another browser → verify the code → sit it, survive a
 * reload → submit → the score lands on the staff side.
 *
 * Mail is read from the core's development mailbox (AM_MAILER=mailbox), which
 * is the only thing here a production deployment does not have.
 *
 * Then the phase 4 gate: a sitting survives its browser being killed during a
 * network outage, with nothing lost.
 *
 *   URL          where the core serves the built studio (default http://localhost:8090)
 *   RUNNER_PORT  the runner's own origin; this test forwards it to the core so
 *                it can cut the candidate's network (default 8091)
 *   STAFF_EMAIL  a staff member of a fresh tenant (scripts/e2e-core.sh makes one)
 *   OUTAGE_SECONDS  how long the candidate's network is down (default 300)
 */
import { chromium } from 'playwright'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL = process.env.URL ?? 'http://localhost:8090'
const RUNNER_PORT = Number(process.env.RUNNER_PORT ?? 8091)
const RUNNER = `http://localhost:${RUNNER_PORT}`
const OUTAGE = Number(process.env.OUTAGE_SECONDS ?? 300)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/**
 * The candidate's network: a TCP forwarder from the runner's origin to the
 * core. Cutting it refuses new connections and drops open ones, which is what
 * a phone losing signal looks like to the browser.
 */
const wire = (() => {
  const [host, port] = new globalThis.URL(URL).host.split(':')
  const open = new Set()
  let server = null
  const up = () =>
    new Promise((resolve) => {
      server = net.createServer((c) => {
        const u = net.connect(Number(port), host)
        open.add(c).add(u)
        const drop = () => { c.destroy(); u.destroy(); open.delete(c); open.delete(u) }
        c.on('error', drop).on('close', drop)
        u.on('error', drop).on('close', drop)
        c.pipe(u).pipe(c)
      })
      server.listen(RUNNER_PORT, '127.0.0.1', resolve)
    })
  const down = () =>
    new Promise((resolve) => {
      for (const s of open) s.destroy()
      open.clear()
      server.close(() => resolve())
    })
  return { up, down }
})()
await wire.up()

/** A browser as a process of its own, so it can be killed like one. */
async function realBrowser(profile) {
  const exe = process.env.CHROMIUM_PATH ?? chromium.executablePath()
  const proc = spawn(exe, ['--headless=new', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0',
    `--user-data-dir=${profile}`, '--window-size=420,900',
    // As Playwright launches it: CI runners and containers often cannot sandbox.
    '--no-sandbox', ...CAMERA, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] })
  const ws = await new Promise((resolve, reject) => {
    let err = ''
    proc.stderr.on('data', (d) => {
      err += d
      const m = err.match(/DevTools listening on (ws:\/\/\S+)/)
      if (m) resolve(m[1])
    })
    proc.on('exit', (c) => reject(new Error(`chromium exited ${c}: ${err.slice(-400)}`)))
  })
  const b = await chromium.connectOverCDP(ws)
  const ctx = b.contexts()[0]
  const p = ctx.pages()[0] ?? (await ctx.newPage())
  p.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message))
  return { proc, b, p }
}
const STAFF = process.env.STAFF_EMAIL
if (!STAFF) throw new Error('STAFF_EMAIL is required')
const run = Date.now().toString(36)
const CANDIDATE = `kandidat.${run}@example.id`

const fails = []
const ok = (m) => console.log(`  \x1b[32m✓\x1b[0m ${m}`)
const bad = (m) => { fails.push(m); console.log(`  \x1b[31m✗\x1b[0m ${m}`) }

/** The newest message to `to` of `kind`, waiting for the mail worker to deliver it. */
async function mail(to, kind, after = 0) {
  for (let i = 0; i < 40; i++) {
    const box = await (await fetch(`${URL}/dev/mailbox`)).json()
    const hit = box.filter((m) => m.To === to && m.Kind === kind)
    if (hit.length > after) return hit[0]
    await new Promise((r) => setTimeout(r, 250))
  }
  throw new Error(`no ${kind} mail for ${to}`)
}
const codeIn = (m) => m.Subject.match(/\d{6}/)[0]

// A fake camera for the tier 2 face check, showing an empty grey room: the
// default test pattern looks enough like a face to the detector to pass as one.
const empty = join(tmpdir(), `am-empty-room-${process.pid}.y4m`)
{
  const [w, h] = [320, 240]
  const frame = Buffer.concat([Buffer.from('FRAME\n'), Buffer.alloc(w * h, 128), Buffer.alloc((w * h) / 2, 128)])
  writeFileSync(empty, Buffer.concat([Buffer.from(`YUV4MPEG2 W${w} H${h} F10:1 Ip A1:1 C420jpeg\n`), frame, frame]))
}
const CAMERA = ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream', `--use-file-for-fake-video-capture=${empty}`]
const browser = await chromium.launch({ args: CAMERA, ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}) })
const staffCtx = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
// The candidate is someone else, on another device: no shared cookies.
const candCtx = await browser.newContext({ viewport: { width: 420, height: 900 } })
const page = await staffCtx.newPage()
const cand = await candCtx.newPage()
const errors = []
for (const p of [page, cand]) {
  p.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message))
  p.on('console', (c) => {
    // A refused request is the point of some checks; the browser logs those itself.
    if (c.type() === 'error' && !/Failed to load resource/.test(c.text())) errors.push(c.text())
  })
}

const nav = async (name) => {
  await page.getByRole('button', { name }).first().click()
  await page.waitForTimeout(400)
}

try {
  /* ---------------- staff sign-in ---------------- */
  await page.goto(URL, { waitUntil: 'networkidle' })
  ;(await page.getByRole('heading', { name: 'Sign in' }).count())
    ? ok('a signed-out visitor gets the sign-in page, not the studio')
    : bad('the studio opened without signing in')
  ;(await page.getByText('Proctor monitor').count()) === 0
    ? ok('design mock-ups are hidden in a core build')
    : bad('a mock-up screen is listed')

  await page.getByLabel('Work email').fill(STAFF)
  await page.getByRole('button', { name: /Email me a code/ }).click()
  const staffCode = codeIn(await mail(STAFF, 'staff-code'))
  await page.getByLabel('Code').fill(staffCode === '000000' ? '111111' : '000000')
  await page.getByRole('button', { name: /^Sign in$/ }).click()
  await page.waitForTimeout(500)
  ;(await page.getByText(/That code is wrong/).count())
    ? ok('a wrong staff code is refused')
    : bad('a wrong staff code was not refused')
  await page.getByLabel('Code').fill(staffCode)
  await page.getByRole('button', { name: /^Sign in$/ }).click()
  await page.waitForTimeout(800)
  ;(await page.getByRole('button', { name: /Sign out/ }).count())
    ? ok('the emailed code signs staff in')
    : bad('staff sign-in failed')

  /* ---------------- author and publish ---------------- */
  await page.getByRole('button', { name: /^New$/ }).first().click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /^Exam/ }).click()
  await page.waitForTimeout(800)
  await page.getByLabel('Exam title').fill(`E2E Core ${run}`)
  await page.getByLabel('Question', { exact: true }).first().fill('Apa keluaran dari len(nil slice) di Go?')
  for (const [i, t] of ['0', 'panic', '-1', 'undefined'].entries())
    await page.getByPlaceholder(`Option ${'ABCD'[i]}`).first().fill(t)
  await page.getByRole('button', { name: /^Correct$/ }).first().click()
  await page.evaluate(async (sel) => {
    const c = document.createElement('canvas')
    c.width = 600; c.height = 300
    const x = c.getContext('2d')
    x.fillStyle = '#2a78d6'; x.fillRect(0, 0, 600, 300)
    const blob = await new Promise((r) => c.toBlob(r, 'image/png'))
    const dt = new DataTransfer()
    dt.items.add(new File([blob], 'diagram.png', { type: 'image/png' }))
    const el = document.querySelector(sel)
    el.files = dt.files
    el.dispatchEvent(new Event('change', { bubbles: true }))
  }, 'input[type="file"][aria-label="Add a figure to the question"]')
  await page.waitForTimeout(1000)
  await page.getByLabel('Image description (alt text)').fill('Diagram alur')
  const src = await page.locator('main img[alt="Diagram alur"]').first().getAttribute('src')
  ;/^\/media\/[0-9a-f-]{36}$/.test(src ?? '')
    ? ok('an image is uploaded to storage; the item keeps only its address')
    : bad(`the image was not uploaded: src=${String(src).slice(0, 40)}`)
  await page.getByRole('button', { name: /^Save draft$/ }).click()
  await page.waitForTimeout(600)
  ;(await page.getByRole('button', { name: /^Saved$/ }).count())
    ? ok('a draft saves to the server')
    : bad(`draft did not save: ${await page.locator('[role=alert]').allInnerTexts()}`)
  await page.getByRole('button', { name: /^Publish v1$/ }).click()
  await page.waitForTimeout(800)

  await page.reload({ waitUntil: 'networkidle' })
  await page.waitForTimeout(600)
  ;(await page.getByText(`E2E Core ${run}`).count())
    ? ok('the instrument survives a reload: it lives on the server')
    : bad('the instrument was not on the server after a reload')

  /* ---------------- QTI out and back in ---------------- */
  await nav(/Library/)
  await page.getByRole('button', { name: new RegExp(`Actions for E2E Core ${run}`) }).click()
  await page.waitForTimeout(250)
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('menuitem', { name: /Export QTI/ }).click()])
  await page.locator('input[aria-label="QTI package"]').setInputFiles(await download.path())
  await page.waitForTimeout(1500)
  await page.getByLabel('Exam title').fill(`Imported ${run}`)
  await page.locator('button[aria-expanded="false"]', { hasText: 'Apa keluaran' }).first().click()
  await page.waitForTimeout(300)
  const back = await page.locator('main img[alt="Diagram alur"]').first().getAttribute('src').catch(() => null)
  const stem = await page.getByLabel('Question', { exact: true }).first().inputValue()
  await page.getByRole('button', { name: /^Save draft$/ }).click()
  await page.waitForTimeout(600)
  ;/^\/media\/[0-9a-f-]{36}$/.test(back ?? '') && back !== src && stem.startsWith('Apa keluaran') &&
  (await page.getByRole('button', { name: /^Saved$/ }).count())
    ? ok('a QTI export imports as a new draft on the server, its figure uploaded again')
    : bad(`QTI import against the core: src=${back} stem=${stem.slice(0, 30)} ${await page.locator('[role=alert]').allInnerTexts()}`)

  /* ---------------- roster and invitation ---------------- */
  await nav(/People/)
  await page.getByRole('button', { name: /^Import$/ }).click()
  await page.waitForTimeout(300)
  await page.getByPlaceholder(/Budi Santoso, budi@example.id/).fill(`Kandidat Uji, ${CANDIDATE}, QA`)
  await page.getByRole('button', { name: /Import roster/ }).click()
  await page.waitForTimeout(600)
  ;(await page.getByText(/Added 1/).count())
    ? ok('roster import writes to the server')
    : bad('roster import failed')

  await nav(/Library/)
  await page.getByRole('button', { name: new RegExp(`Actions for E2E Core ${run}`) }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /Assign people/ }).click()
  await page.waitForTimeout(500)
  await page.getByRole('button', { name: /^Select Kandidat Uji$/ }).click()
  await page.getByRole('button', { name: /^Invite 1 person$/ }).click()
  await page.waitForTimeout(800)
  ;(await page.getByText(/1 email sent/).count())
    ? ok('inviting someone queues their email')
    : bad('the invitation was not sent')

  const invite = await mail(CANDIDATE, 'invite')
  const link = invite.Body.match(/https?:\/\/\S+#\/i\/[A-Za-z0-9_-]{43}/)?.[0]
  link ? ok('the invitation email carries a personal link') : bad('no link in the invitation email')
  link?.startsWith(`${RUNNER}/take/#/i/`)
    ? ok('the link opens the candidate runner, on its own origin')
    : bad(`the link does not open the runner: ${link}`)

  /* ---------------- the candidate, on their own device ---------------- */
  await cand.goto(link, { waitUntil: 'networkidle' })
  await cand.waitForTimeout(600)
  const gateText = await cand.locator('main').innerText()
  gateText.includes('Pastikan ini memang Anda') && !gateText.includes(CANDIDATE)
    ? ok('the link asks for a code and shows only a masked address')
    : bad('the link opened without a code step, or showed the full address')
  ;(await cand.getByText(/Candidate preview/).count()) === 0 && (await cand.getByText('Library').count()) === 0
    ? ok('a candidate sees the assessment alone, with no staff chrome')
    : bad('the candidate saw the studio')

  await cand.getByRole('button', { name: /Kirim kode/ }).click()
  const code = codeIn(await mail(CANDIDATE, 'code'))
  await cand.getByLabel('Kode verifikasi').fill(code === '000000' ? '111111' : '000000')
  await cand.getByRole('button', { name: /^Verifikasi/ }).click()
  await cand.waitForTimeout(500)
  ;(await cand.getByText(/Kode tidak cocok. Sisa 4 dari 5/).count())
    ? ok('a wrong code costs a try and says how many are left')
    : bad('a wrong code was not counted')
  await cand.getByLabel('Kode verifikasi').fill(code)
  await cand.getByRole('button', { name: /^Verifikasi/ }).click()
  await cand.waitForTimeout(800)
  ;(await cand.getByText('email terverifikasi').count())
    ? ok('the right code opens the invitation')
    : bad('verifying the code did not open the invitation')

  const loaded = await cand.evaluate(() => performance.getEntriesByType('resource').map((r) => new globalThis.URL(r.name).pathname))
  loaded.some((p) => p.startsWith('/take/assets/')) && !loaded.some((p) => p.startsWith('/assets/'))
    ? ok('the candidate downloads the runner only, none of the studio')
    : bad(`the candidate loaded studio files: ${loaded.filter((p) => p.startsWith('/assets/')).join(' ')}`)
  ;(await cand.evaluate(() =>
    Promise.race([navigator.serviceWorker.ready.then(() => true), new Promise((r) => setTimeout(() => r(false), 10000))]),
  ))
    ? ok('the runner installs its service worker')
    : bad('no service worker for the runner')

  const twin = await candCtx.newPage()
  await twin.goto(`${RUNNER}/take/#/session`, { waitUntil: 'networkidle' })
  await twin.waitForTimeout(800)
  ;(await twin.getByText('Sedang dibuka di tab lain').count())
    ? ok('a second tab on the same sitting is refused')
    : bad(`a second tab opened the sitting: ${(await twin.locator('main').innerText()).slice(0, 80)}`)
  await twin.close()

  const reuse = await browser.newContext()
  const thief = await reuse.newPage()
  await thief.goto(link, { waitUntil: 'networkidle' })
  await thief.waitForTimeout(600)
  ;(await thief.locator('main').innerText()).includes('Tautan ini sudah dipakai')
    ? ok('the link is spent once its code is verified')
    : bad('a spent link opened again')
  await reuse.close()

  await cand.getByRole('button', { name: /^Mulai$/ }).click()
  await cand.waitForTimeout(300)
  ;(await cand.getByText('Catatan aktivitas pengerjaan').count()) &&
  (await cand.getByText('Layar penuh', { exact: true }).count()) &&
  (await cand.getByText('Pemeriksaan wajah oleh perangkat').count())
    ? ok('a tier 2 exam asks consent for each thing it records, one by one')
    : bad('the consent screen did not list signals, full screen and the camera')
  await cand.getByRole('button', { name: /Setuju dan lanjut/ }).click()
  await cand.waitForTimeout(300)
  await cand.getByText(/Dapat dibuka/).waitFor({ timeout: 8000 }).then(
    () => ok('the readiness check opens the camera for real'),
    () => bad('the readiness check did not find the camera'),
  )
  await cand.getByRole('button', { name: /Mulai sekarang/ }).click()
  await cand.getByText(/^Kamera aktif/).waitFor({ timeout: 20000 }).then(
    () => ok('the face check runs on the device'),
    async () => bad(`the face check did not start: ${await cand.locator('main').innerText()}`),
  )
  // Out of view for a moment: on the record, with how long.
  await cand.evaluate(() => window.dispatchEvent(new Event('blur')))
  await cand.waitForTimeout(1600)
  await cand.evaluate(() => window.dispatchEvent(new Event('focus')))
  ;(await cand.locator('main img[alt="Diagram alur"]').evaluate((i) => i.complete && i.naturalWidth > 0))
    ? ok('the candidate sees the uploaded figure')
    : bad('the figure did not load for the candidate')

  // Pick the right answer, then let the browser die before submitting.
  await cand.locator('main button[aria-pressed]', { hasText: '0' }).first().click()
  await cand.waitForTimeout(1500)
  ;(await cand.getByText(/menunggu sinkron/).count()) === 0
    ? ok('the answer reached the server; nothing is left waiting on the device')
    : bad('the answer was still queued on the device')
  await cand.reload({ waitUntil: 'networkidle' })
  await cand.waitForTimeout(900)
  ;(await cand.getByText(/Anda punya pengerjaan yang tertunda/).count()) && (await cand.getByText(/1 jawaban tersimpan/).count())
    ? ok('a reload mid-exam comes back through the session, with the answer from the server')
    : bad('a reload lost the session or the answer')
  await cand.getByRole('button', { name: /Lanjutkan/ }).click()
  await cand.waitForTimeout(400)
  ;(await cand.locator('main button[aria-pressed="true"]').count())
    ? ok('the saved answer is still selected')
    : bad('the saved answer was not restored')

  // From here to the end of the attempt, no task may hold the main thread
  // past 50 ms (docs/09 phase 4): a frozen tap reads as a lost answer.
  await cand.evaluate(() => {
    window.__long = []
    new PerformanceObserver((l) => window.__long.push(...l.getEntries().map((e) => Math.round(e.duration)))).observe({ type: 'longtask' })
  })

  // The connection drops: work carries on, is kept on the device, and goes when it is back.
  await candCtx.setOffline(true)
  await cand.getByRole('button', { name: /Tandai untuk ditinjau/ }).click()
  await cand.waitForTimeout(1500)
  ;(await cand.getByText(/menunggu sinkron/).count())
    ? ok('offline, the change is kept on the device and says so')
    : bad('offline, nothing said the change was waiting')
  await candCtx.setOffline(false)
  await cand.waitForTimeout(2000)
  ;(await cand.getByText(/menunggu sinkron/).count()) === 0
    ? ok('back online, the waiting change is sent')
    : bad('back online, the change was still waiting')

  const long = await cand.evaluate(() => window.__long)
  long.length === 0
    ? ok('no long task (over 50 ms) while answering')
    : bad(`long tasks while answering: ${long.join(', ')} ms`)

  /* ---------------- the live monitor, while the attempt runs ---------------- */
  await nav(/Proctor monitor/)
  await page.getByRole('combobox', { name: 'Assessment' }).click()
  await page.getByRole('option', { name: `E2E Core ${run}` }).click()
  const sitting = page.getByRole('list', { name: 'Sittings' }).getByRole('button', { name: /Kandidat Uji/ })
  await sitting.waitFor({ timeout: 8000 }).catch(() => {})
  ;(await page.getByText(/Live · 1 in progress/).count()) && /Started/.test(await sitting.innerText().catch(() => ''))
    ? ok('the monitor streams the sitting live, in progress')
    : (await page.screenshot({ path: process.env.SHOT ?? '/dev/null' }), bad(`the monitor did not show the sitting in progress: ${await page.locator('body').innerText()}`))

  // The fake camera shows no face: give the face check a stretch long enough to report.
  await cand.getByText(/^Kamera aktif/).waitFor({ timeout: 20000 }).catch(() => {})
  await cand.waitForTimeout(4500)
  await cand.getByRole('button', { name: /^Selesai/ }).click()
  await cand.waitForTimeout(300)
  await cand.getByRole('button', { name: /^Kumpulkan$/ }).click()
  await cand.waitForTimeout(300)
  await cand.getByRole('button', { name: /Ya, kumpulkan/ }).click()
  await cand.waitForTimeout(1000)
  ;(await cand.getByText(/^100%$/).count()) && (await cand.getByText(/^Lulus$/).count())
    ? ok('the server scores the attempt: 100%, passed')
    : bad(`the score was not 100%: ${await cand.locator('main').innerText()}`)

  /* ---------------- the credential, checked by a stranger ---------------- */
  const credLink = await cand.getByRole('link', { name: /Lihat dan bagikan sertifikat/ }).getAttribute('href').catch(() => null)
  credLink && /\/verify\/#[0-9a-f-]{36}$/.test(credLink)
    ? ok('passing earns an Open Badges credential, linked from the result')
    : bad(`no credential link on the result: ${credLink}`)
  ;(await mail(CANDIDATE, 'credential').then((m) => m.Body.includes(credLink), () => false))
    ? ok('the credential link is mailed to the candidate')
    : bad('no credential mail')
  if (credLink) {
    const strangerCtx = await browser.newContext()
    const stranger = await strangerCtx.newPage()
    stranger.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message))
    await stranger.goto(credLink.replace(/^https?:\/\/[^/]+/, URL))
    await stranger.getByText(/Sertifikat sah/).waitFor({ timeout: 8000 }).then(
      () => ok('anyone can verify it, signature checked in their browser against the issuer key'),
      async () => bad(`the verify page did not accept the credential: ${await stranger.locator('main').innerText()}`),
    )
    await stranger.getByLabel(/Untuk siapa/).fill('someone.else@example.id')
    await stranger.getByRole('button', { name: 'Cocokkan' }).click()
    const wrong = await stranger.getByText(/tidak diterbitkan untuk email tersebut/).count()
    await stranger.getByLabel(/Untuk siapa/).fill(CANDIDATE.toUpperCase())
    await stranger.getByRole('button', { name: 'Cocokkan' }).click()
    ;wrong && (await stranger.getByText(/✓ Sertifikat ini diterbitkan untuk email tersebut/).count())
      ? ok('it names its recipient only as a hash: the right email matches, another does not')
      : bad('the email check on the verify page is wrong')
    ;(await stranger.content()).includes(CANDIDATE)
      ? bad('the verify page shows the email in the clear')
      : ok('the email never appears in the public credential')
    // Someone edits the credential on its way: the name of what was passed.
    await stranger.route('**/credentials/*', async (route) => {
      const res = await route.fetch()
      const body = await res.json()
      const [h, p, sig] = body.jwt.split('.')
      const vc = JSON.parse(Buffer.from(p, 'base64url').toString())
      vc.name = 'Doctor of Medicine'
      body.jwt = [h, Buffer.from(JSON.stringify(vc)).toString('base64url'), sig].join('.')
      await route.fulfill({ response: res, json: body })
    })
    await stranger.reload()
    await stranger.getByText(/tanda tangan sertifikat ini tidak sah/i).waitFor({ timeout: 8000 }).then(
      () => ok('an edited credential fails verification'),
      async () => bad(`an edited credential was accepted: ${await stranger.locator('main').innerText()}`),
    )
    await strangerCtx.close()
  }

  /* ---------------- back on the staff side ---------------- */
  // No reload: the stream brings the hand-in.
  await page.getByText(/Handed in/).first().waitFor({ timeout: 8000 }).then(
    () => ok('the monitor shows the hand-in without a reload'),
    () => bad('the monitor did not update when the attempt was handed in'),
  )
  await sitting.click()
  const record = page.getByRole('list', { name: 'Events' })
  await record.waitFor({ timeout: 5000 }).catch(() => {})
  const said = await record.innerText().catch(() => '')
  ;(await page.getByText(/Record intact/).count()) && /Agreed to: camera/.test(said) && /Came back after \d+ s/.test(said) && /No face in view for \d+ s/.test(said)
    ? ok('the attempt record shows the consents, the time away and the face check, sealed and intact')
    : (await page.screenshot({ path: process.env.SHOT ?? '/dev/null', fullPage: true }), bad(`the attempt record is missing signals: ${said.slice(0, 1600)}`))
  await nav(/Assignments/)
  await page.reload({ waitUntil: 'networkidle' })
  await page.waitForTimeout(600)
  // Through the library: the imported copy is the newer instrument, so it would be the default.
  await nav(/Library/)
  await page.getByRole('button', { name: new RegExp(`Actions for E2E Core ${run}`) }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /Assign people/ }).click()
  await page.waitForTimeout(500)
  ;(await page.getByText('Completed').count()) && (await page.getByText(/^100%$/).count())
    ? ok('the score lands on the assignment row')
    : bad('the assignment did not show the submitted score')
  await page.getByRole('button', { name: 'View answers from Kandidat Uji' }).click()
  await page.getByRole('dialog').getByText(/^Correct$/).first().waitFor({ timeout: 8000 }).then(
    () => ok("the candidate's answers are read back from the server, question by question"),
    async () => bad(`the answer sheet did not load: ${(await page.getByRole('dialog').innerText()).slice(0, 120)}`),
  )
  await page.keyboard.press('Escape')
  await page.waitForTimeout(300)

  /* ---------------- scoring: the worker's item analysis ---------------- */
  await nav(/Scoring/)
  await page.getByRole('combobox', { name: 'Exam' }).click()
  await page.getByRole('option', { name: `E2E Core ${run}` }).click()
  await page.getByText(/Nothing waiting/).waitFor({ timeout: 5000 }).then(
    () => ok('with no essays, the review queue says nothing is waiting'),
    () => bad('the essay review queue did not load'),
  )
  await page.locator('label', { hasText: /^Item analysis$/ }).click()
  const analysis = page.getByRole('table', { name: 'Item analysis' })
  await analysis.waitFor({ timeout: 15000 }).catch(() => {})
  const row = await analysis.innerText().catch(() => '')
  ;/^Q1/m.test(row) && /\b1\.00\b/.test(row) && (await page.getByText(/Provisional: 1 fully scored/).count())
    ? ok('the psychometrics worker analyses the hand-in: Q1 difficulty 1.00, labelled provisional')
    : bad(`no item analysis from the worker: ${row || (await page.locator('main').innerText())}`)

  await nav(/Assignments/)
  await page.reload({ waitUntil: 'networkidle' })
  await page.waitForTimeout(600)
  await nav(/Library/)
  await page.getByRole('button', { name: new RegExp(`Actions for E2E Core ${run}`) }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /Assign people/ }).click()
  await page.waitForTimeout(500)
  const revoke = page.getByRole('button', { name: /Revoke invitation for Kandidat Uji/ })
  ;(await revoke.count()) === 0 || (await revoke.first().isDisabled())
    ? ok('a submitted invitation cannot be revoked')
    : bad('a submitted invitation can still be revoked')

  /* ---------------- the gate: a browser kill during a network outage ---------------- */
  const SECOND = `kandidat2.${run}@example.id`
  await nav(/People/)
  await page.getByRole('button', { name: /^Import$/ }).click()
  await page.waitForTimeout(300)
  await page.getByPlaceholder(/Budi Santoso, budi@example.id/).fill(`Kandidat Dua, ${SECOND}, QA`)
  await page.getByRole('button', { name: /Import roster/ }).click()
  await page.waitForTimeout(600)
  await nav(/Library/)
  await page.getByRole('button', { name: new RegExp(`Actions for E2E Core ${run}`) }).click()
  await page.waitForTimeout(250)
  await page.getByRole('menuitem', { name: /Assign people/ }).click()
  await page.waitForTimeout(500)
  await page.getByRole('button', { name: /^Select Kandidat Dua$/ }).click()
  await page.getByRole('button', { name: /^Invite 1 person$/ }).click()
  await page.waitForTimeout(800)
  const link2 = (await mail(SECOND, 'invite')).Body.match(/https?:\/\/\S+#\/i\/[A-Za-z0-9_-]{43}/)[0]

  const profile = mkdtempSync(join(tmpdir(), 'am-gate-'))
  let real = await realBrowser(profile)
  try {
    const p = real.p
    await p.goto(link2, { waitUntil: 'networkidle' })
    await p.getByRole('button', { name: /Kirim kode/ }).click()
    await p.getByLabel('Kode verifikasi').fill(codeIn(await mail(SECOND, 'code')))
    await p.getByRole('button', { name: /^Verifikasi/ }).click()
    await p.waitForTimeout(800)
    const verifiedAt = Date.now()
    await p.getByRole('button', { name: /^Mulai$/ }).click()
    await p.waitForTimeout(300)
    if (await p.getByRole('button', { name: /Setuju dan lanjut/ }).count()) await p.getByRole('button', { name: /Setuju dan lanjut/ }).click()
    await p.waitForTimeout(300)
    if (await p.getByRole('button', { name: /Mulai sekarang/ }).count()) await p.getByRole('button', { name: /Mulai sekarang/ }).click()
    await p.waitForTimeout(600)
    // A wrong answer reaches the server while the network is up...
    await p.locator('main button[aria-pressed]', { hasText: 'panic' }).first().click()
    await p.waitForTimeout(1500)

    // ...then the network goes. Well into the outage (and at least 30 s after
    // sign-in, since the browser writes cookies back every 30 s) the candidate
    // changes it to the right one, and a second later the browser is killed:
    // too soon for anything written lazily to have reached the disk.
    await wire.down()
    const downAt = Date.now()
    await sleep(Math.max(OUTAGE * 500 - (Date.now() - downAt), 35_000 - (Date.now() - verifiedAt), 0))
    await p.locator('main button[aria-pressed]', { hasText: '0' }).first().click()
    await p.waitForTimeout(1000)
    ;(await p.getByText(/menunggu sinkron/).count())
      ? ok('in the outage, the changed answer waits on the device')
      : bad('in the outage, nothing said the answer was waiting')
    const onDisk = await p.evaluate(async () => {
      const names = []
      for await (const k of (await navigator.storage.getDirectory()).keys()) names.push(k)
      return names.filter((n) => n.startsWith('am.pending.v1.'))
    })
    onDisk.length
      ? ok('the queue is in the origin-private file system, flushed to disk')
      : bad('the queue is not in OPFS')

    real.proc.kill('SIGKILL')
    await new Promise((r) => real.proc.once('exit', r))
    await sleep(Math.max(OUTAGE * 1000 - (Date.now() - downAt), 0))
    await wire.up()
    ok(`the network was down ${Math.round((Date.now() - downAt) / 1000)} s, and the browser was killed during it`)

    real = await realBrowser(profile)
    const q = real.p
    await q.goto(`${RUNNER}/take/#/session`, { waitUntil: 'networkidle' })
    await q.waitForTimeout(1200)
    await q.getByRole('button', { name: /Lanjutkan/ }).click()
    await q.waitForTimeout(2500)
    ;(await q.getByText(/menunggu sinkron/).count()) === 0
      ? ok('reopened, the answer made offline before the kill is sent')
      : bad('reopened, the answer is still waiting')
    await q.getByRole('button', { name: /^Selesai/ }).click()
    await q.waitForTimeout(300)
    await q.getByRole('button', { name: /^Kumpulkan$/ }).click()
    await q.waitForTimeout(300)
    await q.getByRole('button', { name: /Ya, kumpulkan/ }).click()
    await q.waitForTimeout(1200)
    ;(await q.getByText(/^100%$/).count())
      ? ok('GATE: killed during a network outage, the sitting lost nothing (100%)')
      : bad(`GATE: the answer made in the outage was lost: ${(await q.locator('main').innerText()).slice(0, 200)}`)
  } finally {
    real.proc.kill('SIGKILL')
    rmSync(profile, { recursive: true, force: true })
  }

  await page.getByRole('button', { name: /Sign out/ }).first().click()
  await page.waitForTimeout(500)
  ;(await page.getByRole('heading', { name: 'Sign in' }).count())
    ? ok('signing out returns to the sign-in page')
    : bad('sign out did not end the session')
} catch (e) {
  bad(`threw: ${String(e.message).split('\n').slice(0, 6).join(' | ')}`)
} finally {
  if (errors.length) bad(`console/page errors: ${JSON.stringify(errors.slice(0, 3))}`)
  else ok('no console or page errors')
  await browser.close()
  await wire.down().catch(() => {})
}

if (fails.length) {
  console.log(`\n\x1b[31m${fails.length} check(s) failed\x1b[0m`)
  process.exit(1)
}
console.log('\n\x1b[32mall checks passed\x1b[0m')
