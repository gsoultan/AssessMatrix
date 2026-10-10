/**
 * The runner's size budget (docs/09 phase 4): what a candidate's browser must
 * download before the first screen, gzipped. A phone on a slow connection in a
 * regional test centre pays for every kilobyte before the exam even opens.
 *
 * Reads the built runner (dist/take/index.html, or DIST), sums the scripts it
 * loads up front (the entry and its modulepreloads; lazy chunks don't count),
 * and fails over budget.
 */
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'

const DIST = process.env.DIST ?? 'dist'
const BUDGET_KB = 180

const html = readFileSync(join(DIST, 'take/index.html'), 'utf8')
const js = [...html.matchAll(/<(?:script[^>]*\ssrc|link[^>]*rel="modulepreload"[^>]*\shref)="([^"]+\.js)"/g)].map((m) => m[1])
if (!js.length) throw new Error('no scripts found in the runner')
let total = 0
for (const f of js) {
  const kb = gzipSync(readFileSync(join(DIST, f)), { level: 9 }).length / 1024
  total += kb
  console.log(`  ${kb.toFixed(1).padStart(6)} KB  ${f}`)
}
const line = `runner initial JS: ${total.toFixed(1)} KB gzipped (budget ${BUDGET_KB} KB)`
if (total > BUDGET_KB) {
  console.log(`\x1b[31m✗\x1b[0m ${line}`)
  process.exit(1)
}
console.log(`\x1b[32m✓\x1b[0m ${line}`)
