import type { Filter, JobRow, JobState } from '../types'

export type { Filter, JobRow, JobState }

const LIVE: ReadonlySet<string> = new Set(['declared', 'running', 'exited'])
const RECENT_S = 24 * 60 * 60

export type Line = { row: JobRow; depth: number; isContext: boolean }

// The rows the filter keeps, in tree order. Ancestors of a kept row are drawn as
// context so a live child never appears without the job it was forked from. live leaves
// out the rows in stale: a live row nobody has touched within jobs.stale_after is not
// what a person watching the plan is looking for.
export function treeLines(rows: JobRow[], filter: Filter, nowS: number, stale: ReadonlySet<string> = new Set()): Line[] {
  const byId = new Map(rows.map(r => [r.id, r]))
  const matches = (r: JobRow) =>
    filter === 'all' ||
    (filter === 'live' ? LIVE.has(r.state) && !stale.has(r.id) : nowS - r.updated <= RECENT_S)

  const kept = new Set<string>()
  const context = new Set<string>()
  for (const r of rows) {
    if (!matches(r)) continue
    kept.add(r.id)
    for (let p = byId.get(r.parent); p && !kept.has(p.id); p = byId.get(p.parent)) {
      context.add(p.id)
    }
  }

  const children = new Map<string, JobRow[]>()
  const roots: JobRow[] = []
  for (const r of rows) {
    if (!kept.has(r.id) && !context.has(r.id)) continue
    // A parent missing from the store makes the row a root rather than hiding it.
    const parentShown = byId.has(r.parent) && (kept.has(r.parent) || context.has(r.parent))
    if (r.parent && parentShown) {
      children.set(r.parent, [...(children.get(r.parent) ?? []), r])
    } else {
      roots.push(r)
    }
  }

  const byRecent = (a: JobRow, b: JobRow) => b.updated - a.updated || a.id.localeCompare(b.id)
  const out: Line[] = []
  const walk = (r: JobRow, depth: number) => {
    out.push({ row: r, depth, isContext: !kept.has(r.id) })
    for (const c of (children.get(r.id) ?? []).sort(byRecent)) walk(c, depth + 1)
  }
  for (const r of roots.sort(byRecent)) walk(r, 0)
  return out
}

export const GLYPH: Record<JobState, string> = {
  declared: '○',
  running: '●',
  exited: '◐',
  pass: '✓',
  fail: '✗',
  no_return: '⊘',
}

export const COLOR: Record<JobState, string> = {
  declared: 'gray',
  running: 'cyan',
  exited: 'yellow',
  pass: 'green',
  fail: 'red',
  no_return: 'gray',
}

export function age(seconds: number): string {
  if (seconds < 60) return `${Math.max(0, Math.round(seconds))}s`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`
  return `${Math.round(seconds / 86400)}d`
}

// The last segment of an id: a child's full id repeats its parent's, which the
// indentation already shows.
export function leaf(id: string, parent: string): string {
  return parent && id.startsWith(`${parent}/`) ? id.slice(parent.length + 1) : id
}
