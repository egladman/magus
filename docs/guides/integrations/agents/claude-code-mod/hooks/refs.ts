// An output ref (out), guard detail (grd) or invocation id (inv). Each is a
// prefix, then at least 8 lowercase hex or base-36 characters.
const REF = /\b(?:(?:out|grd)[0-9a-f]{8,}|inv[0-9a-z]{8,})\b/g

// refsIn lists the refs a text names, each once, in first-seen order.
export function refsIn(text: string): string[] {
  return [...new Set(text.match(REF) ?? [])]
}

// refArgs is the magus command, after the binary, that prints a ref.
export function refArgs(ref: string): string[] {
  return ref.startsWith('inv') ? ['query', 'invocation', ref] : ['query', 'output', ref]
}

export function refCommand(ref: string): string {
  return ['./magus', ...refArgs(ref)].join(' ')
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

// stripAnsi drops the SGR codes magus colors with; Code draws them as text.
export function stripAnsi(text: string): string {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: ESC is the point.
  return text.replace(/\x1b\[[0-9;]*m/g, '')
}
