/**
 * The same loop as e2e.mjs, against the real Go core and Postgres:
 * sign in by emailed code → author → publish → add someone → invite → open
 * the emailed link in another browser → verify the code → sit it, survive a
 * reload → submit → the score lands on the staff side.
 *
 * Mail is read from the core's development mailbox (AM_MAILER=mailbox), which
 * is the only thing here a production deployment does not have.
 *
 *   URL         where the core serves the built studio (default http://localhost:8090)
 *   STAFF_EMAIL a staff member of a fresh tenant (scripts/e2e-core.sh makes one)
 */
import { chromium } from 'playwright'

const URL = process.env.URL ?? 'http://localhost:8090'
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

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {})
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
  if (await cand.getByRole('button', { name: /Setuju dan lanjut/ }).count())
    await cand.getByRole('button', { name: /Setuju dan lanjut/ }).click()
  await cand.waitForTimeout(300)
  await cand.getByRole('button', { name: /Mulai sekarang/ }).click()
  await cand.waitForTimeout(600)

  // Pick the right answer, then let the browser die before submitting.
  await cand.locator('main button[aria-pressed]', { hasText: '0' }).first().click()
  await cand.waitForTimeout(1500)
  ;(await cand.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('am.pending')).length)) === 0
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

  await cand.getByRole('button', { name: /^Selesai/ }).click()
  await cand.waitForTimeout(300)
  await cand.getByRole('button', { name: /^Kumpulkan$/ }).click()
  await cand.waitForTimeout(300)
  await cand.getByRole('button', { name: /Ya, kumpulkan/ }).click()
  await cand.waitForTimeout(1000)
  ;(await cand.getByText(/^100%$/).count()) && (await cand.getByText(/^Lulus$/).count())
    ? ok('the server scores the attempt: 100%, passed')
    : bad(`the score was not 100%: ${await cand.locator('main').innerText()}`)

  /* ---------------- back on the staff side ---------------- */
  await nav(/Assignments/)
  await page.reload({ waitUntil: 'networkidle' })
  await page.waitForTimeout(600)
  await nav(/Assignments/)
  ;(await page.getByText('Submitted').count()) && (await page.getByText(/^100%$/).count())
    ? ok('the score lands on the assignment row')
    : bad('the assignment did not show the submitted score')

  const revoke = page.getByRole('button', { name: /Revoke invitation for Kandidat Uji/ })
  ;(await revoke.count()) === 0 || (await revoke.first().isDisabled())
    ? ok('a submitted invitation cannot be revoked')
    : bad('a submitted invitation can still be revoked')

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
}

if (fails.length) {
  console.log(`\n\x1b[31m${fails.length} check(s) failed\x1b[0m`)
  process.exit(1)
}
console.log('\n\x1b[32mall checks passed\x1b[0m')
