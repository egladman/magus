import type { Issue } from '../types'

export type { Issue }

export type BinaryInfo = { path: string; version: string; commit: string }

export type ServerInfo = {
  isRunning: boolean
  version: string
  mcpState: string
  mcpNote: string
}

export type Probe = {
  isMagusSource: boolean
  hasLocalBinary: boolean
  pathBinary: string | null
  // The binary the hooks run: ./magus when present, else the one on PATH.
  binary: BinaryInfo | null
  head: string | null
  // Commits from binary.commit to head; null when the commit is not an ancestor of head.
  behind: number | null
  // null when the server could not be asked at all.
  server: ServerInfo | null
}

export const BOOTSTRAP =
  'GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .'

const short = (sha: string) => sha.slice(0, 9)

export function assess(p: Probe): Issue[] {
  const issues: Issue[] = []

  if (!p.hasLocalBinary && p.pathBinary === null) {
    issues.push({
      id: 'guard-off',
      level: 'error',
      label: 'guard off',
      title: 'No magus binary: every hook exits 127 and the guard fails open',
      fix: p.isMagusSource ? BOOTSTRAP : undefined,
    })
    return issues
  }

  if (p.isMagusSource && !p.hasLocalBinary) {
    const built = p.binary ? ` (built at ${short(p.binary.commit)})` : ''
    issues.push({
      id: 'borrowed-binary',
      level: 'warn',
      label: 'no ./magus',
      title: `No ./magus here: hooks run ${p.pathBinary}${built}, with another tree's guard rules`,
      fix: BOOTSTRAP,
    })
  } else if (p.isMagusSource && p.binary && p.head && p.binary.commit !== p.head) {
    const where =
      p.behind === null
        ? `built from ${short(p.binary.commit)}, HEAD is ${short(p.head)}`
        : `${p.behind} commit${p.behind === 1 ? '' : 's'} behind HEAD`
    issues.push({
      id: 'stale-binary',
      level: 'warn',
      label: 'stale ./magus',
      title: `./magus is ${where}`,
      fix: './magus run go-build .',
    })
  }

  const s = p.server
  if (s === null) {
    return issues
  }
  if (!s.isRunning) {
    issues.push({
      id: 'server-down',
      level: 'warn',
      label: 'server down',
      title: 'No magus server: MCP tools, the warm graph and the console are off',
      fix: 'magus server start',
    })
    return issues
  }
  if (p.binary && s.version !== '' && s.version !== p.binary.version) {
    issues.push({
      id: 'server-skew',
      level: 'error',
      label: 'server skew',
      title: `Server runs ${s.version}, binary is ${p.binary.version}: the older build drops fields it cannot decode`,
      fix: 'magus server stop && magus server start',
    })
  }
  if (s.mcpState !== 'serving' && s.mcpState !== 'disabled' && s.mcpState !== '') {
    issues.push({
      id: 'mcp',
      level: 'warn',
      label: `mcp ${s.mcpState}`,
      title: `MCP endpoint is ${s.mcpState}${s.mcpNote ? `: ${s.mcpNote}` : ''}`,
    })
  }
  return issues
}

// The status line entry, which the engine already labels with this mod's name: nothing
// while magus is healthy, the problems' short labels otherwise.
export function statusText(issues: Issue[]): string | undefined {
  if (issues.length === 0) {
    return undefined
  }
  return `⚠ ${issues.map(i => i.label).join(', ')}`
}

// A stable key for the set of issues, so Hide lasts until the set changes.
export function signature(issues: Issue[]): string {
  return issues.map(i => `${i.id}:${i.title}`).join('|')
}
