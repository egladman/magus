import { isRecord, parseJSON } from './result'

export type JsonlError = { line: number; message: string }

// columns holds every key in the order the file first names it; a row's cells
// follow columns, undefined where that entry lacks the key.
export type JsonlTable = {
  columns: string[]
  rows: Array<{ line: number; cells: unknown[] }>
  errors: JsonlError[]
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
      errors.push({ line, message: `not a JSON object: ${compact(parsed.value)}` })
      return
    }
    for (const key of Object.keys(parsed.value)) {
      if (known.has(key)) continue
      known.add(key)
      columns.push(key)
    }
    entries.push({ line, entry: parsed.value })
  })

  const rows = entries.map(({ line, entry }) => ({ line, cells: columns.map(c => entry[c]) }))
  return { columns, rows, errors }
}

// compact draws one value on one line: a string bare, a missing key empty,
// anything else as JSON.
export function compact(v: unknown): string {
  if (v === undefined) return ''
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}

const MAX_CELL = 160

function cell(v: unknown): string {
  const flat = compact(v).replace(/\s+/g, ' ')
  const cut = flat.length > MAX_CELL ? `${flat.slice(0, MAX_CELL - 1)}…` : flat
  return cut.replace(/\\/g, '\\\\').replace(/\|/g, '\\|')
}

// toMarkdown draws a GitHub-flavored markdown table of at most maxRows rows.
// A cell past 160 characters ends in an ellipsis. A closing line counts the
// rows left out. Empty when the file holds no entry.
export function toMarkdown(table: JsonlTable, maxRows: number): string {
  if (table.columns.length === 0) return ''
  const head = `| ${table.columns.map(cell).join(' | ')} |`
  const rule = `| ${table.columns.map(() => '---').join(' | ')} |`
  const shown = table.rows.slice(0, maxRows)
  const body = shown.map(r => `| ${r.cells.map(cell).join(' | ')} |`)
  const left = table.rows.length - shown.length
  const tail = left > 0 ? ['', `${left} more rows not shown.`] : []
  return [head, rule, ...body, ...tail].join('\n')
}

const LINK = /\]\(<?([^)>]+?\.jsonl)>?\)/g
const CODE = /`([^`\n]+?\.jsonl)`/g
const BARE = /(?:^|[\s(])((?:~|\/|\.{1,2}\/)[^\s`'"()<>]*\.jsonl)(?=$|[\s)`'".,;:])/g

// jsonlPaths finds the .jsonl files a markdown text names, each once, in
// first-seen order. It reads link targets, code spans and bare paths. Link
// targets are percent-decoded. A bare path cannot hold a space.
export function jsonlPaths(text: string): string[] {
  const found: string[] = []
  const add = (p: string | undefined) => {
    const path = decode((p ?? '').trim())
    if (path !== '' && !found.includes(path)) found.push(path)
  }
  for (const m of text.matchAll(LINK)) add(m[1])
  for (const m of text.matchAll(CODE)) add(m[1])
  for (const m of text.matchAll(BARE)) add(m[1])
  return found
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

// resolvePath makes a mentioned path absolute: ~ against home, a relative one
// against cwd.
export function resolvePath(path: string, home: string, cwd: string): string {
  if (path === '~' || path.startsWith('~/')) return `${home}${path.slice(1)}`
  if (path.startsWith('/')) return path
  return `${cwd.replace(/\/$/, '')}/${path}`
}
