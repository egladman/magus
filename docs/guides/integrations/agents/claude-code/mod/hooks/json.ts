import { isRecord, parseJSON } from './result'

// Structured is a tool's text read as data. lead and trail are the text around
// one JSON value: an `Exit code 1` line before it, a command's human summary
// after it.
export type Structured =
  | { kind: 'json'; value: object; lead: string; trail: string }
  | { kind: 'jsonl'; records: object[] }

const isTree = (v: unknown): v is object => typeof v === 'object' && v !== null

// closeOf finds where the JSON value opening at start ends, by bracket depth
// outside strings; -1 when it never closes.
function closeOf(text: string, start: number): number {
  let depth = 0
  let inString = false
  for (let i = start; i < text.length; i++) {
    const c = text[i]
    if (inString) {
      if (c === '\\') i++
      else if (c === '"') inString = false
      continue
    }
    if (c === '"') inString = true
    else if (c === '{' || c === '[') depth++
    else if (c === '}' || c === ']') {
      depth--
      if (depth === 0) return i
    }
  }
  return -1
}

function jsonl(text: string): object[] | null {
  const lines = text.split('\n').filter(l => l.trim() !== '')
  if (lines.length < 2) return null
  const records: object[] = []
  for (const line of lines) {
    const parsed = parseJSON(line)
    if (parsed.error || !isTree(parsed.value)) return null
    records.push(parsed.value)
  }
  return records
}

// A value opens a line, so a `{` inside prose is no match.
const OPENER = /(?:^|\n)[ \t]*([{[])/

// structuredOf reads text as one JSON object or array, possibly between a
// leading line and trailing prose, or as JSON Lines of at least two records.
// null when it is neither: a bare scalar is not structured output.
export function structuredOf(text: string): Structured | null {
  const whole = parseJSON(text)
  if (!whole.error) return isTree(whole.value) ? { kind: 'json', value: whole.value, lead: '', trail: '' } : null
  const records = jsonl(text)
  if (records !== null) return { kind: 'jsonl', records }
  const m = OPENER.exec(text)
  if (m === null) return null
  const start = m.index + m[0].length - 1
  const lead = text.slice(0, start).trim()
  // More than one line of prose before it is a reply that quotes JSON.
  if (lead.includes('\n')) return null
  const end = closeOf(text, start)
  if (end < 0) return null
  const parsed = parseJSON(text.slice(start, end + 1))
  if (parsed.error || !isTree(parsed.value)) return null
  return { kind: 'json', value: parsed.value, lead, trail: text.slice(end + 1).trim() }
}

// headerOf names a record by its envelope: `api_version · kind · name`, the
// parts it carries. null when it carries no api_version or kind, and for an
// empty api_version with no kind, as a job row's nested record has.
export function headerOf(v: unknown): string | null {
  if (!isRecord(v)) return null
  const parts: string[] = []
  const version = v.api_version ?? v.apiVersion
  if (typeof version === 'string' && version !== '') parts.push(version)
  if (typeof v.kind === 'string' && v.kind !== '') parts.push(v.kind)
  if (parts.length === 0) return null
  const meta = isRecord(v.metadata) ? v.metadata : v
  const name = meta.name ?? meta.id
  if (typeof name === 'string' && name !== '') parts.push(name)
  return parts.join(' · ')
}

// summaryOf is what a collapsed object or array draws in place of its body.
export function summaryOf(v: object): string {
  if (Array.isArray(v)) return `[…] ${v.length} item${v.length === 1 ? '' : 's'}`
  const n = Object.keys(v).length
  return `{…} ${n} key${n === 1 ? '' : 's'}`
}

// childPath joins a node path the way the open set stores it, one segment per
// key or index, so a key holding `/` cannot collide with a nested one.
export function childPath(path: string, key: string | number): string {
  return `${path}/${encodeURIComponent(String(key))}`
}

export type Leaf = { text: string; type: 'string' | 'number' | 'boolean' | 'null' }

const MAX_LEAF = 240

// leafOf draws a scalar as JSON does, a long string cut at MAX_LEAF code
// points; the raw view holds it whole.
export function leafOf(v: unknown): Leaf {
  if (v === null || v === undefined) return { text: 'null', type: 'null' }
  if (typeof v === 'number') return { text: String(v), type: 'number' }
  if (typeof v === 'boolean') return { text: String(v), type: 'boolean' }
  const s = JSON.stringify(String(v))
  const points = [...s]
  return { text: points.length > MAX_LEAF ? `${points.slice(0, MAX_LEAF - 2).join('')}…"` : s, type: 'string' }
}

// textsOf finds the text a tool result printed: Bash's stdout, an MCP tool's
// or a subagent's text blocks, or a bare string (an errored call's text).
// Anything else is a structured record the engine draws itself.
export function textsOf(output: unknown): { text: string; stderr: string } | null {
  if (typeof output === 'string') return { text: output, stderr: '' }
  const blocks = Array.isArray(output) ? output : isRecord(output) ? output.content : undefined
  if (Array.isArray(blocks)) {
    const texts = blocks.filter(b => isRecord(b) && b.type === 'text' && typeof b.text === 'string')
    if (texts.length === 0) return null
    return { text: texts.map(b => (b as { text: string }).text).join('\n'), stderr: '' }
  }
  if (isRecord(output) && typeof output.stdout === 'string') {
    return { text: output.stdout, stderr: typeof output.stderr === 'string' ? output.stderr : '' }
  }
  return null
}
