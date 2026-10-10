/**
 * QTI 3.0 packages in and out (docs/09 phase 3).
 *
 * A package is a zip: imsmanifest.xml, one assessment test, one file per item,
 * and the images they show. QTI carries the substance of an instrument — the
 * stems, options, correct answers, points, sections and time limits — and any
 * QTI tool can read that much. What QTI has no word for (questionnaire vs
 * exam, integrity tier, pass mark, skills, scale labels) rides in a sidecar,
 * assessmatrix.json, which other tools ignore and this one reads back, so our
 * own exports round-trip without loss.
 *
 * Import reads 3.0 and the 2.x element names, since most item banks in the
 * wild are still 2.1. A package is someone else's file: it is read with the
 * same caps as a roster, text is only ever taken as text, and an image is only
 * ever a file inside the package, re-encoded on the way in like any upload.
 */
import { ingestImage, type Media } from './media'
import { type Exam, type Item, type Kind, type Section, uid } from './store'
import { writeZip, ZipError, type ZipEntry, zipBytes, zipEntries } from './zip'

export const MAX_PACKAGE_BYTES = 50 * 1024 * 1024
const MAX_ENTRY_BYTES = 10 * 1024 * 1024
const MAX_ITEMS = 500

export class QtiError extends Error {}

const NS = 'http://www.imsglobal.org/xsd/imsqtiasi_v3p0'
const CP = 'http://www.imsglobal.org/xsd/imscp_v1p1'
const MATCH_CORRECT = 'https://purl.imsglobal.org/spec/qti/v3p0/rptemplates/match_correct.xml'
const MAP_RESPONSE = 'https://purl.imsglobal.org/spec/qti/v3p0/rptemplates/map_response.xml'
const SIDECAR = 'assessmatrix.json'

/** What QTI cannot say. Keyed by our own ids, so a foreign tool's edits to the XML still win. */
type Sidecar = {
  assessmatrix: 1
  exam: Pick<Exam, 'kind' | 'issuer' | 'tier' | 'passPct' | 'shuffle' | 'timed' | 'anonymous'>
  sections: Record<string, { minutes: number }>
  items: Record<string, Pick<Item, 'type' | 'skill'> & Partial<Pick<Item, 'required' | 'allowAnswerImage' | 'scale'>>>
}

/* ── export ───────────────────────────────────────────────────────────────── */

/** Our ids are [A-Za-z0-9_-]; a QTI identifier may not start with a digit or '-'. */
const qid = (id: string) => (/^[A-Za-z_]/.test(id) ? id : `_${id}`)
const unqid = (id: string) => (/^_[0-9-]/.test(id) ? id.slice(1) : id)

const esc = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/** One paragraph per line, so a stem's line breaks survive the trip. */
const paras = (s: string) =>
  s
    .split('\n')
    .map((l) => `<p>${esc(l)}</p>`)
    .join('')

const EXT: Record<string, string> = { 'image/webp': 'webp', 'image/jpeg': 'jpg', 'image/png': 'png', 'image/gif': 'gif' }

type Packed = { path: string; bytes: Uint8Array }

/** Every image the exam shows, fetched once each and given a path in the package. */
async function packMedia(exam: Exam): Promise<Map<string, Packed>> {
  const out = new Map<string, Packed>()
  const all = exam.sections.flatMap((s) => s.items.flatMap((i) => [i.image, ...i.choices.map((c) => c.image)]))
  for (const m of all) {
    if (!m || out.has(m.url)) continue
    const res = await fetch(m.url, { credentials: 'same-origin' })
    if (!res.ok) throw new QtiError(`An image in this exam could not be read (${res.status}).`)
    const blob = await res.blob()
    const ext = EXT[blob.type] ?? 'bin'
    out.set(m.url, { path: `media/${out.size + 1}.${ext}`, bytes: new Uint8Array(await blob.arrayBuffer()) })
  }
  return out
}

const img = (m: Media | undefined, media: Map<string, Packed>) =>
  m ? `<img src="../${media.get(m.url)!.path}" alt="${esc(m.alt)}" width="${m.w}" height="${m.h}"/>` : ''

const scaleChoices = (it: Item) => {
  const n = it.scale?.points ?? 5
  return Array.from({ length: n }, (_, k) => ({
    id: `v${k + 1}`,
    text: k === 0 ? (it.scale?.minLabel ?? '1') : k === n - 1 ? (it.scale?.maxLabel ?? String(n)) : String(k + 1),
  }))
}

function itemXml(it: Item, scored: boolean, media: Map<string, Packed>): string {
  const choice = it.type === 'single' || it.type === 'multiple' || it.type === 'boolean'
  const scale = it.type === 'likert' || it.type === 'rating'
  const text = it.type === 'short' || it.type === 'essay'
  const cardinality = it.type === 'multiple' ? 'multiple' : 'single'
  const baseType = text ? 'string' : 'identifier'
  const keys = scored && (choice || it.type === 'short') ? it.correct : []

  let decl = `<qti-response-declaration identifier="RESPONSE" cardinality="${cardinality}" base-type="${baseType}">`
  if (keys.length)
    decl += `<qti-correct-response>${keys.map((k) => `<qti-value>${esc(choice ? qid(k) : k)}</qti-value>`).join('')}</qti-correct-response>`
  if (it.type === 'short' && keys.length)
    decl += `<qti-mapping default-value="0" upper-bound="${it.points}">${keys
      .map((k) => `<qti-map-entry map-key="${esc(k)}" mapped-value="${it.points}" case-sensitive="false"/>`)
      .join('')}</qti-mapping>`
  decl += '</qti-response-declaration>'

  const outcomes =
    '<qti-outcome-declaration identifier="SCORE" cardinality="single" base-type="float"><qti-default-value><qti-value>0</qti-value></qti-default-value></qti-outcome-declaration>' +
    `<qti-outcome-declaration identifier="MAXSCORE" cardinality="single" base-type="float"><qti-default-value><qti-value>${it.points}</qti-value></qti-default-value></qti-outcome-declaration>`

  let interaction = ''
  if (choice || scale) {
    const opts = scale ? scaleChoices(it) : it.choices
    const max = it.type === 'multiple' ? 0 : 1
    interaction =
      `<qti-choice-interaction response-identifier="RESPONSE" max-choices="${max}" shuffle="false">` +
      opts
        .map((c) => `<qti-simple-choice identifier="${esc(qid(c.id))}">${esc(c.text)}${img((c as { image?: Media }).image, media)}</qti-simple-choice>`)
        .join('') +
      '</qti-choice-interaction>'
  } else if (it.type === 'short') {
    interaction = '<p><qti-text-entry-interaction response-identifier="RESPONSE"/></p>'
  } else {
    interaction = '<qti-extended-text-interaction response-identifier="RESPONSE"/>'
  }

  const processing = !keys.length ? '' : `<qti-response-processing template="${it.type === 'short' ? MAP_RESPONSE : MATCH_CORRECT}"/>`

  return (
    '<?xml version="1.0" encoding="UTF-8"?>\n' +
    `<qti-assessment-item xmlns="${NS}" identifier="${esc(qid(it.id))}" title="${esc(it.stem.split('\n')[0]!.slice(0, 80))}" adaptive="false" time-dependent="false">` +
    decl +
    outcomes +
    `<qti-item-body><div class="stem">${paras(it.stem)}${img(it.image, media)}</div>${interaction}</qti-item-body>` +
    processing +
    '</qti-assessment-item>\n'
  )
}

function testXml(exam: Exam): string {
  const sections = exam.sections
    .map(
      (s) =>
        `<qti-assessment-section identifier="${esc(qid(s.id))}" title="${esc(s.name)}" visible="true">` +
        (exam.shuffle ? '<qti-ordering shuffle="true"/>' : '') +
        (exam.timed ? `<qti-time-limits max-time="${s.minutes * 60}"/>` : '') +
        s.items
          .map((i) => `<qti-assessment-item-ref identifier="${esc(qid(i.id))}" href="items/${esc(qid(i.id))}.xml"/>`)
          .join('') +
        '</qti-assessment-section>',
    )
    .join('')
  return (
    '<?xml version="1.0" encoding="UTF-8"?>\n' +
    `<qti-assessment-test xmlns="${NS}" identifier="${esc(qid(exam.id))}" title="${esc(exam.title)}">` +
    '<qti-test-part identifier="part1" navigation-mode="nonlinear" submission-mode="simultaneous">' +
    sections +
    '</qti-test-part></qti-assessment-test>\n'
  )
}

function manifestXml(exam: Exam, media: Map<string, Packed>): string {
  const items = exam.sections.flatMap((s) => s.items)
  const res = (id: string, type: string, href: string, deps: string[] = []) =>
    `<resource identifier="${esc(id)}" type="${type}" href="${esc(href)}"><file href="${esc(href)}"/>${deps
      .map((d) => `<dependency identifierref="${esc(d)}"/>`)
      .join('')}</resource>`
  return (
    '<?xml version="1.0" encoding="UTF-8"?>\n' +
    `<manifest xmlns="${CP}" identifier="M-${esc(qid(exam.id))}"><metadata><schema>QTI Package</schema><schemaversion>3.0.0</schemaversion></metadata><organizations/><resources>` +
    res('TEST', 'imsqti_test_xmlv3p0', 'test.xml', items.map((i) => `R-${qid(i.id)}`)) +
    items.map((i) => res(`R-${qid(i.id)}`, 'imsqti_item_xmlv3p0', `items/${qid(i.id)}.xml`)).join('') +
    [...media.values()].map((m) => res(`R-${m.path.replace(/\W/g, '-')}`, 'webcontent', m.path)).join('') +
    res('SIDECAR', 'webcontent', SIDECAR) +
    '</resources></manifest>\n'
  )
}

function sidecar(exam: Exam): Sidecar {
  const items: Sidecar['items'] = {}
  for (const s of exam.sections)
    for (const i of s.items)
      items[i.id] = {
        type: i.type,
        skill: i.skill,
        ...(i.required !== undefined && { required: i.required }),
        ...(i.allowAnswerImage !== undefined && { allowAnswerImage: i.allowAnswerImage }),
        ...(i.scale && { scale: i.scale }),
      }
  return {
    assessmatrix: 1,
    exam: {
      kind: exam.kind,
      issuer: exam.issuer,
      tier: exam.tier,
      passPct: exam.passPct,
      shuffle: exam.shuffle,
      timed: exam.timed,
      anonymous: exam.anonymous,
    },
    sections: Object.fromEntries(exam.sections.map((s) => [s.id, { minutes: s.minutes }])),
    items,
  }
}

/** The exam as a QTI 3.0 content package. */
export async function exportQti(exam: Exam): Promise<Blob> {
  const media = await packMedia(exam)
  const enc = new TextEncoder()
  const scored = exam.kind !== 'survey'
  const files: [string, Uint8Array][] = [
    ['imsmanifest.xml', enc.encode(manifestXml(exam, media))],
    ['test.xml', enc.encode(testXml(exam))],
    ...exam.sections.flatMap((s) =>
      s.items.map((i): [string, Uint8Array] => [`items/${qid(i.id)}.xml`, enc.encode(itemXml(i, scored, media))]),
    ),
    ...[...media.values()].map((m): [string, Uint8Array] => [m.path, m.bytes]),
    [SIDECAR, enc.encode(`${JSON.stringify(sidecar(exam), null, 2)}\n`)],
  ]
  return writeZip(files)
}

export const qtiFileName = (exam: Exam) =>
  `${exam.title.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '').toLowerCase() || 'exam'}.qti.zip`

/* ── import ───────────────────────────────────────────────────────────────── */

/** `qti-choice-interaction` (3.0) and `choiceInteraction` (2.x) are the same element. */
const local = (el: Element) =>
  el.localName.startsWith('qti-') ? el.localName.slice(4) : el.localName.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`)

/** Likewise `response-identifier` and `responseIdentifier`. */
const attr = (el: Element, name: string) =>
  el.getAttribute(name) ?? el.getAttribute(name.replace(/-([a-z])/g, (_, c: string) => c.toUpperCase()))

const kids = (el: Element, name: string) => [...el.children].filter((c) => local(c) === name)
const kid = (el: Element, name: string) => kids(el, name)[0]
const find = (el: Element, name: string): Element[] => [...el.getElementsByTagName('*')].filter((c) => local(c) === name)

/** Our id charset, so a foreign identifier passes the core's validation. */
const cleanId = (s: string | null, prefix: string) => {
  const id = unqid((s ?? '').replace(/[^A-Za-z0-9_-]/g, '_')).slice(0, 64)
  return id || uid(prefix)
}

const resolve = (base: string, href: string) => {
  const parts = base.split('/').slice(0, -1)
  for (const p of href.split('?')[0]!.split('/')) {
    if (p === '..') parts.pop()
    else if (p && p !== '.') parts.push(decodeURIComponent(p))
  }
  return parts.join('/')
}

function parseXml(text: string, what: string): Element {
  const doc = new DOMParser().parseFromString(text, 'application/xml')
  if (doc.getElementsByTagName('parsererror').length) throw new QtiError(`${what} is not well-formed XML.`)
  return doc.documentElement
}

/** Visible text of an element, minus interactions and images; one line per paragraph. */
function textOf(el: Element): string {
  const lines: string[] = []
  let line = ''
  const flush = (force = false) => {
    if (force || line.trim()) lines.push(line)
    line = ''
  }
  const walk = (n: Node) => {
    if (n.nodeType === Node.TEXT_NODE) {
      line += n.textContent ?? ''
      return
    }
    if (n.nodeType !== Node.ELEMENT_NODE) return
    const e = n as Element
    const name = local(e)
    if (name.endsWith('interaction') || name === 'img' || name === 'prompt' || name.startsWith('feedback')) return
    const block = name === 'p' || name === 'div' || name === 'li' || name === 'br'
    if (block) flush()
    for (const c of e.childNodes) walk(c)
    // An empty <p/> is a deliberate blank line, since ours writes one per line
    // of the stem; a <p> that only held an inline interaction is not.
    if (block) flush(name === 'p' && ![...e.getElementsByTagName('*')].some((x) => local(x).endsWith('interaction')))
  }
  for (const c of el.childNodes) walk(c)
  flush()
  // Our paragraphs never contain a newline or a tab, so one that does is a
  // foreign file's indentation, not content.
  return lines
    .map((l) => (/[\n\r\t]/.test(l) ? l.replace(/\s+/g, ' ').trim() : l))
    .join('\n')
}

type Package = {
  buf: ArrayBuffer
  entries: Map<string, ZipEntry>
  images: Map<string, Media>
  adopt: (blob: Blob, name: string, alt: string) => Promise<Media>
}

async function bytes(pkg: Package, path: string): Promise<Uint8Array | undefined> {
  const e = pkg.entries.get(path)
  return e && zipBytes(pkg.buf, e, MAX_ENTRY_BYTES)
}

async function text(pkg: Package, path: string): Promise<string | undefined> {
  const b = await bytes(pkg, path)
  return b && new TextDecoder().decode(b)
}

/** An <img> inside the package, brought in as an upload would be. Anything else is dropped. */
async function image(pkg: Package, base: string, el: Element | undefined): Promise<Media | undefined> {
  if (!el) return undefined
  const src = el.getAttribute('src') ?? ''
  if (/^[a-z][a-z0-9+.-]*:/i.test(src)) return undefined
  const path = resolve(base, src)
  const alt = el.getAttribute('alt') ?? ''
  const cached = pkg.images.get(path)
  if (cached) return { ...cached, alt }
  const b = await bytes(pkg, path)
  if (!b) return undefined
  const m = await pkg.adopt(new Blob([b as BlobPart]), path.split('/').pop()!, alt)
  pkg.images.set(path, m)
  return m
}

const num = (s: string | null | undefined) => {
  const n = Number(s)
  return Number.isFinite(n) ? n : undefined
}

async function readItem(pkg: Package, path: string, side: Sidecar | undefined, warn: (s: string) => void): Promise<Item | undefined> {
  const xml = await text(pkg, path)
  if (xml === undefined) {
    warn(`${path} is listed but missing from the package.`)
    return undefined
  }
  const root = parseXml(xml, path)
  if (local(root) !== 'assessment-item') {
    warn(`${path} is not an assessment item.`)
    return undefined
  }
  const id = cleanId(attr(root, 'identifier'), 'i')
  const extra = side?.items[id]
  const body = find(root, 'item-body')[0]
  if (!body) {
    warn(`${path} has no item body.`)
    return undefined
  }

  const interactions = find(body, 'choice-interaction')
    .concat(find(body, 'text-entry-interaction'))
    .concat(find(body, 'extended-text-interaction'))
  if (interactions.length === 0) {
    const other = [...body.getElementsByTagName('*')].find((e) => local(e).endsWith('-interaction'))
    warn(`${path} uses an interaction this app does not support${other ? ` (${local(other)})` : ''}; skipped.`)
    return undefined
  }
  if (interactions.length > 1) warn(`${path} has more than one interaction; only the first is kept.`)
  const inter = interactions[0]!
  const kind = local(inter)
  const rid = attr(inter, 'response-identifier')
  const decl = find(root, 'response-declaration').find((d) => attr(d, 'identifier') === rid)
  const cardinality = decl && attr(decl, 'cardinality')
  const correct = decl ? find(decl, 'correct-response').flatMap((c) => find(c, 'value').map((v) => (v.textContent ?? '').trim())) : []
  const mapped = decl ? find(decl, 'map-entry').map((m) => attr(m, 'map-key') ?? '') : []
  const maxScore = find(root, 'outcome-declaration')
    .filter((o) => attr(o, 'identifier') === 'MAXSCORE')
    .flatMap((o) => find(o, 'value'))
    .map((v) => num(v.textContent))[0]
  const mapMax = decl && find(decl, 'mapping').map((m) => num(attr(m, 'upper-bound')))[0]

  // The stem is everything in the body that is not the interaction; a 2.x prompt counts too.
  const prompt = kid(inter, 'prompt')
  const stem = [textOf(body), prompt ? textOf(prompt) : ''].filter(Boolean).join('\n')
  const stemImg = find(body, 'img').find((i) => !i.closest('[response-identifier],[responseIdentifier]'))
  const item: Item = {
    id,
    type: 'single',
    stem,
    choices: [],
    correct: [],
    points: Math.max(0, Math.round(maxScore ?? mapMax ?? 1)),
    skill: extra?.skill ?? '',
  }
  const pic = await image(pkg, path, stemImg)
  if (pic) item.image = pic

  if (kind === 'choice-interaction') {
    const max = num(attr(inter, 'max-choices')) ?? 1
    const multiple = cardinality === 'multiple' || max !== 1
    const simple = find(inter, 'simple-choice')
    for (const c of simple) {
      const cid = cleanId(attr(c, 'identifier'), 'c')
      const choice: Item['choices'][number] = { id: cid, text: textOf(c) }
      const cimg = await image(pkg, path, find(c, 'img')[0])
      if (cimg) choice.image = cimg
      item.choices.push(choice)
    }
    item.correct = correct.map((c) => cleanId(c, 'c')).filter((c) => item.choices.some((x) => x.id === c))
    item.type = multiple ? 'multiple' : 'single'
    if (extra?.type === 'boolean' || extra?.type === 'likert' || extra?.type === 'rating') item.type = extra.type
    if (item.type === 'likert' || item.type === 'rating') {
      item.scale = extra?.scale ?? {
        points: item.choices.length,
        minLabel: item.choices[0]?.text ?? '',
        maxLabel: item.choices.at(-1)?.text ?? '',
      }
      item.choices = []
      item.correct = []
    }
  } else if (kind === 'text-entry-interaction') {
    item.type = 'short'
    item.correct = [...new Set([...correct, ...mapped].filter(Boolean))]
  } else {
    item.type = 'essay'
  }
  if (extra?.required !== undefined) item.required = extra.required
  if (extra?.allowAnswerImage !== undefined) item.allowAnswerImage = extra.allowAnswerImage
  return item
}

/** Sections in document order; a nested section's items join its outermost parent. */
async function readTest(
  pkg: Package,
  path: string,
  side: Sidecar | undefined,
  warn: (s: string) => void,
): Promise<{ title: string; sections: Section[]; timed: boolean; shuffle: boolean }> {
  const root = parseXml((await text(pkg, path))!, path)
  if (local(root) !== 'assessment-test') throw new QtiError(`${path} is not an assessment test.`)
  const testTl = find(root, 'time-limits').find((t) => local(t.parentElement!) !== 'assessment-section')
  const testLimit = num(testTl && attr(testTl, 'max-time'))
  const sections: Section[] = []
  let timed = false
  let shuffle = false
  let count = 0
  const tops = find(root, 'assessment-section').filter((s) => !s.parentElement || local(s.parentElement) !== 'assessment-section')
  for (const s of tops) {
    const sid = cleanId(attr(s, 'identifier'), 's')
    const tl = kid(s, 'time-limits')
    const limit = num(tl && attr(tl, 'max-time'))
    if (limit) timed = true
    if (find(s, 'ordering').some((o) => o.getAttribute('shuffle') === 'true')) shuffle = true
    const items: Item[] = []
    for (const ref of find(s, 'assessment-item-ref')) {
      if (++count > MAX_ITEMS) throw new QtiError(`This package has more than ${MAX_ITEMS} questions.`)
      const it = await readItem(pkg, resolve(path, attr(ref, 'href') ?? ''), side, warn)
      if (it) items.push(it)
    }
    if (!items.length) continue
    const minutes = side?.sections[sid]?.minutes ?? (limit ? Math.max(1, Math.round(limit / 60)) : 30)
    sections.push({ id: sid, name: s.getAttribute('title') || `Bagian ${sections.length + 1}`, minutes, items })
  }
  if (testLimit && sections.length) {
    timed = true
    if (!sections.some((s) => side?.sections[s.id])) {
      const each = Math.max(1, Math.round(testLimit / 60 / sections.length))
      for (const s of sections) s.minutes = each
    }
  }
  return { title: root.getAttribute('title') ?? '', sections, timed, shuffle }
}

export type Imported = { exam: Exam; warnings: string[] }

/**
 * A QTI package → a new draft instrument. `adopt` turns an image in the
 * package into Media; the default re-encodes and, against the core, uploads it.
 */
export async function importQti(
  file: Blob,
  adopt: Package['adopt'] = (blob, name, alt) => ingestImage(new File([blob], name, { type: typeOf(name) }), alt),
): Promise<Imported> {
  if (file.size > MAX_PACKAGE_BYTES) throw new QtiError('That package is larger than 50 MB.')
  const buf = await file.arrayBuffer()
  let list: ZipEntry[]
  try {
    list = zipEntries(buf)
  } catch (e) {
    throw e instanceof ZipError ? new QtiError('That is not a QTI package (.zip).') : e
  }
  const pkg: Package = { buf, entries: new Map(list.map((e) => [e.name, e])), images: new Map(), adopt }
  const warnings: string[] = []
  const warn = (s: string) => warnings.push(s)

  try {
    const sideText = await text(pkg, SIDECAR)
    let side: Sidecar | undefined
    if (sideText)
      try {
        const s = JSON.parse(sideText) as Sidecar
        if (s?.assessmatrix === 1) side = s
      } catch {
        warn(`${SIDECAR} could not be read; using QTI only.`)
      }

    // The manifest names the test; without one, any test file will do.
    let testPath: string | undefined
    let itemPaths: string[] = []
    const manifest = await text(pkg, 'imsmanifest.xml')
    if (manifest) {
      const resources = find(parseXml(manifest, 'imsmanifest.xml'), 'resource')
      testPath = resources.find((r) => /test/.test(r.getAttribute('type') ?? ''))?.getAttribute('href') ?? undefined
      itemPaths = resources.filter((r) => /item/.test(r.getAttribute('type') ?? '')).map((r) => r.getAttribute('href') ?? '')
    }
    if (!testPath && !itemPaths.length)
      for (const name of pkg.entries.keys()) {
        if (!name.endsWith('.xml') || name === 'imsmanifest.xml') continue
        const head = (await text(pkg, name))!.slice(0, 2000)
        if (/assessment-?[tT]est/.test(head)) testPath ??= name
        else if (/assessment-?[iI]tem/.test(head)) itemPaths.push(name)
      }

    let title = ''
    let sections: Section[]
    let timed = false
    let shuffle = false
    if (testPath) ({ title, sections, timed, shuffle } = await readTest(pkg, testPath, side, warn))
    else {
      if (itemPaths.length > MAX_ITEMS) throw new QtiError(`This package has more than ${MAX_ITEMS} questions.`)
      const items: Item[] = []
      for (const p of itemPaths) {
        const it = await readItem(pkg, p, side, warn)
        if (it) items.push(it)
      }
      sections = items.length ? [{ id: uid('s'), name: 'Bagian 1', minutes: 30, items }] : []
    }
    if (!sections.length) throw new QtiError('That package has no questions this app can read.')

    const anyKey = sections.some((s) => s.items.some((i) => i.correct.length))
    const kind: Kind = side?.exam.kind ?? (anyKey ? 'exam' : 'survey')
    const exam: Exam = {
      id: uid('e'),
      kind,
      status: 'draft',
      version: 1,
      title: title || (file instanceof File ? file.name.replace(/\.(qti\.)?zip$/i, '') : '') || 'Imported',
      issuer: side?.exam.issuer ?? '',
      tier: side?.exam.tier ?? (kind === 'exam' ? 2 : 0),
      passPct: side?.exam.passPct ?? 70,
      shuffle: side?.exam.shuffle ?? shuffle,
      timed: side?.exam.timed ?? timed,
      anonymous: side?.exam.anonymous ?? kind === 'survey',
      sections,
      updatedAt: Date.now(),
    }
    return { exam, warnings }
  } catch (e) {
    if (e instanceof ZipError)
      throw new QtiError(e.fault === 'too-large' ? 'A file in that package is too large.' : 'That package is damaged.')
    throw e
  }
}

const typeOf = (name: string) => {
  const ext = name.split('.').pop()?.toLowerCase()
  return ext === 'jpg' || ext === 'jpeg' ? 'image/jpeg' : ext ? `image/${ext}` : 'image/png'
}
