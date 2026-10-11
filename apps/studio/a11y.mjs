/**
 * The candidate's side, checked for WCAG 2.2 AA and walked by keyboard alone.
 *
 * For each seeded instrument (between them every item type), every screen a
 * candidate sees is scanned with axe, and every question is answered and
 * left using only Tab, Space, Enter and typing. A candidate who cannot use a
 * mouse must be able to finish; a screen reader user must be told what each
 * control is.
 *
 *   URL   the dev server (scripts/e2e.sh starts one)
 */
import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'

const URL = process.env.URL ?? 'http://localhost:5180'
const AXE = readFileSync(createRequire(import.meta.url).resolve('axe-core/axe.min.js'), 'utf8')
const fails = []
const ok = (m) => console.log(`  \x1b[32m✓\x1b[0m ${m}`)
const bad = (m) => { fails.push(m); console.log(`  \x1b[31m✗\x1b[0m ${m}`) }

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {})
// A phone: most candidates sit on one.
const ctx = await browser.newContext({ viewport: { width: 400, height: 860 } })
const page = await ctx.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(e.message))

/** axe on what the candidate sees now; serious and critical violations fail. */
const seen = new Map()
async function scan(where) {
  if (!(await page.evaluate(() => 'axe' in window))) await page.addScriptTag({ content: AXE })
  const r = await page.evaluate(() =>
    window.axe.run(document.querySelector('main') ?? document, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'] },
    }),
  )
  for (const v of r.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')) {
    const key = `${v.id}`
    if (seen.has(key)) continue
    seen.set(key, where)
    bad(`axe ${v.impact}: ${v.id} on ${where} — ${v.help} (${v.nodes.slice(0, 2).map((n) => `${n.target.join(' ')}: ${(n.any[0]?.message ?? '').slice(0, 120)}`).join('; ')})`)
  }
}

/** What has focus, in words, for a failure message. */
const focused = () =>
  page.evaluate(() => {
    const el = document.activeElement
    return el ? `${el.tagName.toLowerCase()} "${(el.getAttribute('aria-label') ?? el.textContent ?? '').trim().slice(0, 40)}"` : 'nothing'
  })

/** Tab until focus satisfies `want` (evaluated in the page), at most 60 presses. */
async function tabTo(want, arg) {
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press('Tab')
    if (await page.evaluate(want, arg)) return true
  }
  return false
}

const isAnswer = () => {
  const el = document.activeElement
  if (!el || !el.closest('main')) return false
  return (el.matches('button[aria-pressed], [role=radio], [role=checkbox]') ||
    el.matches('textarea, input[type=text], input:not([type])')) && !el.closest('[data-chrome]')
}
const isButton = (name) => {
  const el = document.activeElement
  return !!el && el.tagName === 'BUTTON' && new RegExp(name).test(el.textContent ?? '')
}

/** Answer the question on screen with the keyboard; true if the answer took. */
async function answerByKeyboard() {
  if (!(await tabTo(isAnswer))) return `no answer control reachable by Tab (focus ends on ${await focused()})`
  const kind = await page.evaluate(() => document.activeElement.tagName)
  if (kind === 'BUTTON') {
    await page.keyboard.press('Space')
    await page.waitForTimeout(120)
    return (await page.evaluate(() => document.activeElement.getAttribute('aria-pressed'))) === 'true'
      ? true
      : `Space on ${await focused()} did not select it`
  }
  await page.keyboard.type('jawaban dari papan ketik')
  await page.waitForTimeout(120)
  return (await page.evaluate(() => document.activeElement.value)).includes('papan ketik') ? true : 'typing did not reach the field'
}

/** Press a button by its text, reached by Tab (then Shift+Tab, if it is behind focus). */
async function pressByKeyboard(name) {
  if (await page.evaluate(isButton, name)) return page.keyboard.press('Enter')
  let found = await tabTo(isButton, name)
  if (!found)
    for (let i = 0; i < 60 && !found; i++) {
      await page.keyboard.press('Shift+Tab')
      found = await page.evaluate(isButton, name)
    }
  if (!found) throw new Error(`"${name}" is not reachable by keyboard`)
  await page.keyboard.press('Enter')
}

async function sit(title, start) {
  // The library is a desktop screen; the candidate's is then a phone.
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto(URL, { waitUntil: 'networkidle' })
  await page.getByRole('button', { name: new RegExp(`Preview ${title}`) }).click()
  await page.setViewportSize({ width: 400, height: 860 })
  await page.waitForTimeout(500)
  await scan(`${title}: start`)
  await pressByKeyboard(start)
  await page.waitForTimeout(400)
  if (await page.getByRole('button', { name: /Setuju dan lanjut/ }).count()) {
    await scan(`${title}: consent`)
    // Consent is a decision: a box to tick before the button does anything.
    const box = page.locator('main input[type=checkbox]')
    for (let i = 0; i < (await box.count()); i++) {
      await box.nth(i).focus()
      await page.keyboard.press('Space')
    }
    await pressByKeyboard('Setuju dan lanjut')
    await page.waitForTimeout(400)
  }
  // Only an exam that uses the camera has a device check of its own; the rest start from the start page.
  if (await page.getByRole('button', { name: /Mulai sekarang/ }).count()) {
    await scan(`${title}: before starting`)
    await pressByKeyboard('Mulai sekarang')
    await page.waitForTimeout(500)
  }

  const total = Number((await page.getByText(/Pertanyaan 1 dari \d+/i).first().innerText()).match(/dari (\d+)/i)[1])
  const types = new Set()
  let answered = 0
  for (let i = 0; i < total; i++) {
    types.add(await page.locator('main [data-item-type]').first().getAttribute('data-item-type').catch(() => '?'))
    await scan(`${title}: question ${i + 1}`)
    // Start from the top of the question, as a keyboard user arriving would.
    await page.locator('main h2, main [data-item-type]').first().evaluate((el) => {
      el.setAttribute('tabindex', '-1')
      el.focus()
    })
    const r = await answerByKeyboard()
    if (r === true) answered++
    else bad(`${title}, question ${i + 1}: ${r}`)
    if (i < total - 1) {
      await pressByKeyboard('Berikutnya')
      await page.waitForTimeout(250)
    }
  }
  answered === total
    ? ok(`${title}: all ${total} questions answered by keyboard alone (${[...types].join(', ')})`)
    : bad(`${title}: ${answered} of ${total} answered by keyboard`)
  await pressByKeyboard('Selesai')
  await page.waitForTimeout(300)
  await scan(`${title}: review`)
}

try {
  await sit('Sertifikasi Backend Engineer — Level 3', 'Mulai')
  await sit('Kuesioner Pengalaman Onboarding', 'Mulai mengisi')
  if (!fails.some((f) => f.startsWith('axe'))) ok('axe: no serious or critical WCAG 2.2 AA violations on any candidate screen')
} catch (e) {
  bad(`threw: ${String(e.message).split('\n').slice(0, 4).join(' | ')}`)
} finally {
  if (errors.length) bad(`page errors: ${JSON.stringify(errors.slice(0, 3))}`)
  await browser.close()
}

if (fails.length) {
  console.log(`\n\x1b[31m${fails.length} check(s) failed\x1b[0m`)
  process.exit(1)
}
console.log('\n\x1b[32mall checks passed\x1b[0m')
