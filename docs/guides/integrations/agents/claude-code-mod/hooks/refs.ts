// Copied from magus, which the mod cannot import. IsMintedRef in
// internal/cache/output.go: out, then 12 or 8 hex. namesRef in
// internal/guard/cost.go: grd, then 16 hex; an inv id needs a digit.
const REF = /\b(?:out(?:[0-9a-f]{12}|[0-9a-f]{8})|grd[0-9a-f]{16}|inv[0-9a-z]+)\b/g

// refsIn lists the refs a text names, each once, in first-seen order.
export function refsIn(text: string): string[] {
  const refs = text.match(REF) ?? []
  return [...new Set(refs.filter(r => !r.startsWith('inv') || /\d/.test(r)))]
}

// refStrings gathers the raw string fields of a tool output. Scanning the
// JSON text instead hides a ref behind the escape of the newline before it.
export function refStrings(value: unknown, into: string[] = []): string[] {
  if (typeof value === 'string') into.push(value)
  else if (Array.isArray(value)) for (const v of value) refStrings(v, into)
  else if (typeof value === 'object' && value !== null) for (const v of Object.values(value)) refStrings(v, into)
  return into
}

// refArgs is the magus command, after the binary, that prints a ref.
export function refArgs(ref: string): string[] {
  return ref.startsWith('inv') ? ['query', 'invocation', ref] : ['query', 'output', ref]
}

export function refCommand(ref: string, program = './magus'): string {
  return [program, ...refArgs(ref)].join(' ')
}

// The page that says what a ref is. A surface that opens a link itself,
// so no press reaches the mod, lands there.
export const REF_DOC = 'https://eli.gladman.cc/magus/concepts/cache/output-refs/'

// refLinks draws refs as one markdown line of links. A link's title is the
// command that prints its ref, which a surface may show on hover.
export function refLinks(refs: string[]): string {
  return refs.map(r => `[${r}](${REF_DOC}#${r} "${refCommand(r)}")`).join(' · ')
}

// refOf reads the ref back from a link refLinks drew; null for any other.
export function refOf(href: string): string | null {
  if (!href.startsWith(`${REF_DOC}#`)) return null
  const ref = href.slice(REF_DOC.length + 1)
  return refsIn(ref)[0] === ref ? ref : null
}

// stripAnsi drops every CSI and OSC sequence; Code draws them as text.
export function stripAnsi(text: string): string {
  return text.replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '').replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
}
