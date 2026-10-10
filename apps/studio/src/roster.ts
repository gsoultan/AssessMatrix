/**
 * Reading a roster out of a file: CSV, or the first sheet of an .xlsx.
 *
 * The file is someone else's data, usually exported from an HR system, so it
 * is read defensively: size, row and decompression caps before anything is
 * parsed, and only three columns are ever kept — name, email, group. A roster
 * export routinely carries a date of birth or a national id in column F; a
 * column this file does not recognise is dropped here and never stored.
 *
 * The .xlsx reader is written out rather than imported: a workbook is a zip of
 * XML, the browser already has an inflater and an XML parser, and the forty
 * lines between them are cheaper to own than a spreadsheet library is to audit.
 * It reads cell text. It does not evaluate formulas, follow links or load
 * anything the file points at.
 */
import type { Person, PersonDraft } from './store'

import { type ZipEntry, type ZipFault, ZipError, zipEntries as readEntries, zipText as readText } from './zip'

export const MAX_FILE_BYTES = 5 * 1024 * 1024
export const MAX_ROWS = 5000
/** Per zip entry, after inflation. A workbook is the classic zip bomb. */
const MAX_INFLATED_BYTES = 30 * 1024 * 1024

export class RosterFileError extends Error {}

/* ── CSV ──────────────────────────────────────────────────────────────────── */

/** Whichever of comma, semicolon or tab the first line uses most. Excel in Indonesia writes semicolons. */
function sniffDelimiter(text: string): string {
  const first = text.split(/\r?\n/, 1)[0] ?? ''
  const count = (d: string) => first.split(d).length - 1
  return [',', ';', '\t'].reduce((best, d) => (count(d) > count(best) ? d : best), ',')
}

/** RFC 4180: quoted fields, doubled quotes, line breaks inside quotes. */
export function parseCsv(input: string): string[][] {
  const text = input.replace(/^﻿/, '')
  const delim = sniffDelimiter(text)
  const rows: string[][] = []
  let row: string[] = []
  let cell = ''
  let quoted = false

  for (let i = 0; i < text.length; i++) {
    const ch = text[i]!
    if (quoted) {
      if (ch === '"' && text[i + 1] === '"') {
        cell += '"'
        i++
      } else if (ch === '"') quoted = false
      else cell += ch
      continue
    }
    if (ch === '"' && cell === '') quoted = true
    else if (ch === delim) {
      row.push(cell)
      cell = ''
    } else if (ch === '\n' || ch === '\r') {
      if (ch === '\r' && text[i + 1] === '\n') i++
      row.push(cell)
      rows.push(row)
      if (rows.length > MAX_ROWS + 1) throw new RosterFileError(`More than ${MAX_ROWS} rows. Split the file and import it in parts.`)
      row = []
      cell = ''
    } else cell += ch
  }
  if (cell !== '' || row.length > 0) {
    row.push(cell)
    rows.push(row)
  }
  return rows
}

/* ── XLSX ─────────────────────────────────────────────────────────────────── */

/** A ZIP fault, in words about a workbook. */
const WORKBOOK: Record<ZipFault, string> = {
  'not-zip': 'That is not an Excel workbook (.xlsx).',
  damaged: 'That workbook is damaged.',
  encrypted: 'That workbook is password-protected. Save a copy without the password.',
  'too-large': 'That workbook is too large to read here.',
  method: 'That workbook uses a compression this reader does not support.',
}

const asWorkbook = <T>(p: () => T): T => {
  try {
    return p()
  } catch (e) {
    throw e instanceof ZipError ? new RosterFileError(WORKBOOK[e.fault]) : e
  }
}

const zipEntries = (buf: ArrayBuffer) => asWorkbook(() => readEntries(buf))
const zipText = (buf: ArrayBuffer, e: ZipEntry) =>
  readText(buf, e, MAX_INFLATED_BYTES).catch((err) => {
    throw err instanceof ZipError ? new RosterFileError(WORKBOOK[err.fault]) : err
  })

function xml(text: string): Document {
  const doc = new DOMParser().parseFromString(text, 'application/xml')
  if (doc.getElementsByTagName('parsererror').length) throw new RosterFileError('That workbook is damaged.')
  return doc
}

/** "C7" → 2. Cells are sparse, so position comes from the reference, not the order. */
function columnOf(ref: string): number {
  let n = 0
  for (const ch of ref) {
    const c = ch.charCodeAt(0)
    if (c < 65 || c > 90) break
    n = n * 26 + (c - 64)
  }
  return n - 1
}

/** A string's runs, without the phonetic guide East Asian workbooks attach. */
const runText = (el: Element) =>
  [...el.getElementsByTagName('t')]
    .filter((t) => t.parentElement?.localName !== 'rPh')
    .map((t) => t.textContent ?? '')
    .join('')

/** Rows of the workbook's first sheet, as text. */
export async function readXlsx(buf: ArrayBuffer): Promise<string[][]> {
  const entries = zipEntries(buf)
  const sheet = entries
    .filter((e) => /^xl\/worksheets\/sheet\d+\.xml$/.test(e.name))
    .sort((a, b) => Number(a.name.match(/\d+/)![0]) - Number(b.name.match(/\d+/)![0]))[0]
  if (!sheet) throw new RosterFileError('That workbook has no sheet to read.')

  const stringsEntry = entries.find((e) => e.name === 'xl/sharedStrings.xml')
  const strings = stringsEntry
    ? [...xml(await zipText(buf, stringsEntry)).getElementsByTagName('si')].map(runText)
    : []

  const rows: string[][] = []
  for (const r of xml(await zipText(buf, sheet)).getElementsByTagName('row')) {
    const row: string[] = []
    for (const c of r.getElementsByTagName('c')) {
      const type = c.getAttribute('t')
      const v = c.getElementsByTagName('v')[0]?.textContent ?? ''
      const text = type === 's' ? strings[Number(v)] ?? '' : type === 'inlineStr' ? runText(c) : v
      const col = columnOf(c.getAttribute('r') ?? '')
      row[col >= 0 ? col : row.length] = text
    }
    rows.push(Array.from(row, (x) => x ?? ''))
    if (rows.length > MAX_ROWS + 1) throw new RosterFileError(`More than ${MAX_ROWS} rows. Split the file and import it in parts.`)
  }
  return rows
}

/* ── rows → people ────────────────────────────────────────────────────────── */

export type Rejected = { row: number; reason: string; shown: string }

export type RosterReport = {
  /** New to the roster. */
  fresh: PersonDraft[]
  /** In the file and already on the roster — not added again, but still invitable. */
  known: PersonDraft[]
  /** The same address further up the same file. */
  repeated: number
  rejected: Rejected[]
}

const EMAIL = /^[^\s@<>(),;:"]+@[^\s@<>(),;:"]+\.[^\s@<>(),;:"]{2,}$/
const HEAD = {
  email: /^(e-?mail|surel|email address|alamat e-?mail)$/i,
  name: /^(name|nama|full ?name|nama lengkap|candidate|kandidat|peserta)$/i,
  group: /^(group|grup|kelompok|kelas|class|cohort|angkatan|department|departemen|divisi|division|team|tim)$/i,
}

/** Whitespace collapsed, control characters gone, length capped. */
const clean = (s: string | undefined, max: number) =>
  (s ?? '').replace(/[\u0000-\u001f\u007f]/g, ' ').replace(/\s+/g, ' ').trim().slice(0, max)

/**
 * With a header row the columns are taken by name, so their order and any
 * extra columns do not matter. Without one the cell that looks like an address
 * is the email and the next two are name and group, as when pasting.
 */
export function rowsToRoster(rows: string[][], roster: Person[]): RosterReport {
  const report: RosterReport = { fresh: [], known: [], repeated: 0, rejected: [] }
  const body = rows.map((cells, i) => ({ cells: cells.map((c) => c.trim()), n: i + 1 })).filter((r) => r.cells.some(Boolean))
  if (body.length === 0) return report

  const head = body[0]!.cells
  const emailCol = head.findIndex((h) => HEAD.email.test(h))
  const headed = emailCol >= 0
  const nameCol = headed ? head.findIndex((h) => HEAD.name.test(h)) : -1
  const groupCol = headed ? head.findIndex((h) => HEAD.group.test(h)) : -1

  const onRoster = new Set(roster.map((p) => p.email.toLowerCase()))
  const inFile = new Set<string>()

  for (const { cells, n } of headed ? body.slice(1) : body) {
    let email: string
    let name: string
    let group: string
    if (headed) {
      email = cells[emailCol] ?? ''
      name = nameCol >= 0 ? cells[nameCol] ?? '' : ''
      group = groupCol >= 0 ? cells[groupCol] ?? '' : ''
    } else {
      const at = cells.findIndex((c) => c.includes('@'))
      const rest = cells.filter((_, i) => i !== at)
      email = at >= 0 ? cells[at]! : ''
      name = rest[0] ?? ''
      group = rest[1] ?? ''
    }

    email = clean(email, 254)
    if (!EMAIL.test(email)) {
      report.rejected.push({
        row: n,
        reason: email ? 'not an email address' : 'no email address',
        shown: clean(cells.filter(Boolean).join(', '), 80),
      })
      continue
    }
    const key = email.toLowerCase()
    if (inFile.has(key)) {
      report.repeated++
      continue
    }
    inFile.add(key)
    const draft = { email, name: clean(name, 120), group: clean(group, 80) }
    ;(onRoster.has(key) ? report.known : report.fresh).push(draft)
  }
  return report
}

/** A file from the picker, read as rows. The extension decides; the bytes are checked by the reader. */
export async function readRosterFile(file: File): Promise<string[][]> {
  if (file.size > MAX_FILE_BYTES) throw new RosterFileError('That file is larger than 5 MB. Split it and import it in parts.')
  const name = file.name.toLowerCase()
  if (name.endsWith('.xlsx')) return readXlsx(await file.arrayBuffer())
  if (name.endsWith('.xls'))
    throw new RosterFileError('That is the old Excel format (.xls). Save it as .xlsx or .csv and try again.')
  if (/\.(csv|tsv|txt)$/.test(name)) return parseCsv(await file.text())
  throw new RosterFileError('Choose a .csv or .xlsx file.')
}
