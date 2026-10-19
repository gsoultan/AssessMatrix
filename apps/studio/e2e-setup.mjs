/**
 * A fresh install, first run, in a browser: the core started on an empty
 * database sets up its own schema, says in its log where to finish, and the
 * first visitor is sent to /setup. A wrong setup code is refused; the right
 * one creates the super administrator and the default tenant and signs them
 * straight into the studio. After that /setup is closed, and says so.
 *
 *   URL         where the core serves the built studio
 *   SETUP_CODE  the code the core printed (scripts/e2e-setup.sh reads its log)
 */
import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'

const URL = process.env.URL ?? 'http://localhost:8093'
const CODE = process.env.SETUP_CODE
if (!CODE) throw new Error('SETUP_CODE is required')
const AXE = readFileSync(createRequire(import.meta.url).resolve('axe-core/axe.min.js'), 'utf8')

const fails = []
const ok = (m) => console.log(`  \x1b[32m✓\x1b[0m ${m}`)
const bad = (m) => { fails.push(m); console.log(`  \x1b[31m✗\x1b[0m ${m}`) }
const check = (cond, good, fail) => (cond ? ok(good) : bad(fail ?? `NOT: ${good}`))

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {})
// bypassCSP lets the test inject axe; the page itself runs under the real policy's rules otherwise.
const page = await (await browser.newContext({ viewport: { width: 1280, height: 900 }, bypassCSP: true })).newPage()
const errors = []
page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message))
page.on('console', (c) => {
  if (c.type() === 'error' && !/Failed to load resource/.test(c.text())) errors.push(c.text())
})

try {
  await page.goto(URL + '/')
  await page.waitForURL('**/setup', { timeout: 10_000 }).catch(() => {})
  check(new globalThis.URL(page.url()).pathname === '/setup', 'the first visitor to a fresh install is sent to /setup')
  await page.getByRole('heading', { name: 'Set up AssessMatrix' }).waitFor({ timeout: 10_000 })
  check(await page.getByText(/schema up to date/).isVisible(), 'the page shows the database connected and its schema up to date')

  await page.addScriptTag({ content: AXE })
  const axe = await page.evaluate(() =>
    window.axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'] } }),
  )
  const serious = axe.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  check(serious.length === 0, 'axe finds no serious or critical WCAG problem on /setup', `axe: ${serious.map((v) => v.id).join(', ')}`)

  await page.getByLabel('Setup code').fill('AAAA-BBBB-CCCC')
  await page.getByLabel('Default tenant').fill('Lembaga Sertifikasi Uji')
  await page.getByLabel('Your name').fill('Admin Pertama')
  await page.getByLabel('Your work email').fill('admin@lembaga.example.id')
  await page.getByRole('button', { name: /Create super administrator/ }).click()
  await page.getByText('That setup code is wrong').waitFor({ timeout: 10_000 }).then(
    () => ok('a wrong setup code is refused, with where to find the right one'),
    () => bad('a wrong setup code was not refused'),
  )

  // Typed as read off a log: lower case, spaces for dashes.
  await page.getByLabel('Setup code').fill(CODE.toLowerCase().replaceAll('-', ' '))
  await page.getByRole('button', { name: /Create super administrator/ }).click()
  await page.getByText('Lembaga Sertifikasi Uji').first().waitFor({ timeout: 15_000 }).then(
    () => ok('the right code creates the default tenant and signs the super administrator into the studio'),
    () => bad('setup did not end signed in to the studio'),
  )
  check(await page.getByText('Super administrator').first().isVisible(), 'the studio shows them as the super administrator')
  check(new globalThis.URL(page.url()).pathname === '/', 'the address is back to / after setup')

  await page.reload()
  await page.getByText('Lembaga Sertifikasi Uji').first().waitFor({ timeout: 10_000 }).then(
    () => ok('the administrator stays signed in across a reload'),
    () => bad('the session from setup did not survive a reload'),
  )

  await page.goto(URL + '/setup')
  await page.getByRole('heading', { name: 'Already set up' }).waitFor({ timeout: 10_000 }).then(
    () => ok('/setup is closed once done, and says so'),
    () => bad('/setup still offers setup after it was done'),
  )
  const again = await page.evaluate(async (code) => {
    const r = await fetch('/assessmatrix.v1.SetupService/CompleteSetup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ setupCode: code, organizationName: 'Second', adminEmail: 'second@example.id' }),
    })
    return (await r.json()).code
  }, CODE)
  check(again === 'failed_precondition', 'the server refuses a second setup even with the right code', `second setup answered ${again}`)
} catch (e) {
  bad('crashed: ' + e.message)
} finally {
  await browser.close()
}

if (errors.length) bad('console or page errors: ' + errors.join(' | '))
else ok('no console or page errors')
if (fails.length) {
  console.log(`\n\x1b[31m${fails.length} check(s) failed\x1b[0m`)
  process.exit(1)
}
console.log('\n\x1b[32mall checks passed\x1b[0m')
