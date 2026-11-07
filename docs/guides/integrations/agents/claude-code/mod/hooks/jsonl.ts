import { isRecord, parseJSON } from './result'

type JsonlError = { line: number; message: string }

// columns holds every key in the order the file first names it; a row's cells
// follow columns, undefined where that entry lacks the key.
export type JsonlTable = {
  columns: string[]
  rows: Array<{ line: number; cells: unknown[] }>
  errors: JsonlError[]
}

const MAX_CELL = 160
const MAX_ECHOED = 80

// truncate cuts by code point, so it never splits a surrogate pair.
function truncate(text: string, max: number): string {
  if (text.length <= max) return text
  const points = [...text]
  return points.length <= max ? text : `${points.slice(0, max - 1).join('')}…`
}

// parseJsonl reads every line of a JSON Lines file. A line that is not a JSON
// object becomes an error with its 1-based number; blank lines carry no entry.
export function parseJsonl(text: string): JsonlTable {
  const columns: string[] = []
  const known = new Set<string>()
  const entries: Array<{ line: number; entry: Record<string, unknown> }> = []
  const errors: JsonlError[] = []

  text.split('\n').forEach((raw, i) => {
    const line = i + 1
    if (raw.trim() === '') return
    const parsed = parseJSON(raw)
    if (parsed.error) {
      errors.push({ line, message: parsed.error.message })
      return
    }
    if (!isRecord(parsed.value)) {
      errors.push({ line, message: `not a JSON object: ${truncate(compact(parsed.value), MAX_ECHOED)}` })
      return
    }
    for (const key of Object.keys(parsed.value)) {
      if (known.has(key)) continue
      known.add(key)
      columns.push(key)
    }
    entries.push({ line, entry: parsed.value })
  })

  const rows = entries.map(({ line, entry }) => ({
    line,
    cells: columns.map(c => (Object.hasOwn(entry, c) ? entry[c] : undefined)),
  }))
  return { columns, rows, errors }
}

// compact draws one value on one line: a string bare, a missing key empty,
// anything else as JSON.
function compact(v: unknown): string {
  if (typeof v === 'string') return v
  return JSON.stringify(v) ?? ''
}

// Markdown would read these as links, emphasis, HTML or entities.
const SPECIAL = /[\\|[\]()<>`*_!&]/g

function cell(v: unknown): string {
  const flat = compact(v).replace(/\s+/g, ' ')
  return truncate(flat, MAX_CELL).replace(SPECIAL, '\\$&')
}

// toMarkdown draws a GitHub-flavored markdown table of at most maxRows rows
// and maxColumns columns. A cell past 160 characters ends in an ellipsis.
// Closing lines count the rows and columns left out. Empty when the file
// holds no entry.
export function toMarkdown(table: JsonlTable, maxRows: number, maxColumns: number): string {
  if (table.columns.length === 0) return ''
  const columns = table.columns.slice(0, maxColumns)
  const head = `| ${columns.map(cell).join(' | ')} |`
  const rule = `| ${columns.map(() => '---').join(' | ')} |`
  const shown = table.rows.slice(0, maxRows)
  const body = shown.map(r => `| ${r.cells.slice(0, maxColumns).map(cell).join(' | ')} |`)
  const rowsLeft = table.rows.length - shown.length
  const colsLeft = table.columns.length - columns.length
  const tail = [
    ...(rowsLeft > 0 ? [`${rowsLeft} more rows not shown.`] : []),
    ...(colsLeft > 0 ? [`${colsLeft} more columns not shown.`] : []),
  ]
  return [head, rule, ...body, ...(tail.length > 0 ? ['', ...tail] : [])].join('\n')
}

// A bare path ends at a period only before a space, so a.jsonl.bak is no match.
const LINK = /\]\(<?([^)>]+?\.jsonl)>?\)/g
const CODE = /`([^`\n]+?\.jsonl)`/g
const BARE = /(?:^|[\s(])((?:~|\/|\.{1,2}\/)[^\s`'"()<>]*\.jsonl)(?=$|[\s)`'",;:!?]|\.(?:$|\s))/g
const SCHEME = /^[a-z][a-z0-9+.-]*:/i
const ANCHORED = /^(?:~|\.{1,2})?\//
const GLOB = /[*?[\]{}]/

// jsonlPaths finds the .jsonl files a markdown text names, each once, in
// first-seen order. It reads link targets, code spans and bare paths.
// A link with a scheme other than file: is skipped. A code span holds a path
// only without glob characters, and without whitespace unless it starts with
// ~/, / or ./ so that `jq . a.jsonl` is a command, not a path.
export function jsonlPaths(text: string): string[] {
  const found: string[] = []
  const add = (path: string) => {
    if (path !== '' && !found.includes(path)) found.push(path)
  }
  for (const m of text.matchAll(LINK)) add(linkPath(m[1] ?? ''))
  for (const m of text.matchAll(CODE)) add(codePath(m[1] ?? ''))
  for (const m of text.matchAll(BARE)) add(decode((m[1] ?? '').trim()))
  return found
}

function linkPath(target: string): string {
  const t = target.trim()
  if (SCHEME.test(t) && !t.startsWith('file:')) return ''
  return decode(t)
}

function codePath(span: string): string {
  const t = span.trim()
  if (GLOB.test(t)) return ''
  if (/\s/.test(t) && !ANCHORED.test(t)) return ''
  return t
}

function decode(p: string): string {
  const path = p.startsWith('file://') ? p.slice('file://'.length) : p
  try {
    return decodeURIComponent(path)
  } catch {
    // A lone % is a literal character in a path, not an encoding.
    return path
  }
}

// resolvePath makes a mentioned path absolute and normal: ~ against home, a
// relative one against cwd, with . and .. segments and doubled slashes folded.
export function resolvePath(path: string, home: string, cwd: string): string {
  let full = path
  if (path === '~' || path.startsWith('~/')) full = `${home}${path.slice(1)}`
  else if (!path.startsWith('/')) full = `${cwd}/${path}`
  const parts: string[] = []
  for (const part of full.split('/')) {
    if (part === '' || part === '.') continue
    if (part === '..') parts.pop()
    else parts.push(part)
  }
  return `/${parts.join('/')}`
}
