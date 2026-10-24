import type { Budget, BudgetHolder, BudgetRead, Refusal } from '../types'
import { age } from './tree'

export type { Budget, BudgetHolder, BudgetRead, Refusal }

export type Parsed = { ok: true; budget: Budget | null } | { ok: false; error: string }

// How long the band names a refusal after it happened.
export const REFUSAL_SHOW_MS = 5 * 60_000

const REFUSED = '[MGS3009]'

// jsonv2 leaves zero values out, so an absent count is 0.
const int = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const time = (v: unknown): number => (typeof v === 'string' ? Date.parse(v) || 0 : 0)
const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

// The broker's build budget from `magus status -o json`. budget is null when the report
// has no broker, which is a budget nothing holds.
export function parseBudget(stdout: string): Parsed {
  let doc: unknown
  try {
    doc = JSON.parse(stdout)
  } catch {
    return { ok: false, error: `magus status -o json printed no JSON${stdout ? `: ${stdout.slice(0, 120)}` : ''}` }
  }
  if (!isRecord(doc)) return { ok: false, error: 'magus status -o json printed no object' }
  if (doc.broker == null) return { ok: true, budget: null }
  if (!isRecord(doc.broker) || !isRecord(doc.broker.capacity)) {
    return { ok: false, error: 'magus status -o json has a broker without broker.capacity' }
  }
  const c = doc.broker.capacity
  const holders: BudgetHolder[] = (Array.isArray(c.holders) ? c.holders : []).filter(isRecord).map(h => ({
    pid: int(h.pid),
    project: str(h.project),
    target: str(h.target),
    dir: str(h.dir),
    command: str(h.command),
    slots: int(h.slots),
    memoryMb: int(h.memory_mb),
    since: time(h.since),
  }))
  return {
    ok: true,
    budget: {
      budgetSlots: int(c.budget_slots),
      heldSlots: int(c.held_slots),
      budgetMb: int(c.budget_mb),
      heldMb: int(c.held_mb),
      holders,
    },
  }
}

export function gib(mb: number): string {
  return (mb / 1024).toFixed(1)
}

export function capacityText(b: Budget): string {
  return `${b.heldSlots}/${b.budgetSlots} slots, ${gib(b.heldMb)}/${gib(b.budgetMb)} GiB`
}

// The status line entry: undefined before the first read.
export function budgetStatus(read: BudgetRead | null): string | undefined {
  if (read === null) return undefined
  return read.budget === null ? 'budget idle' : `budget ${capacityText(read.budget)}`
}

// The last path segment: the checkout's name, which is what tells two worktrees apart.
export function checkout(dir: string): string {
  return dir.replace(/\/+$/, '').split('/').at(-1) ?? dir
}

export function holderText(h: BudgetHolder, now: number): string {
  const what = h.project && h.project !== '.' ? `${h.project} ${h.target}` : h.target
  return [
    `pid ${h.pid}`,
    what,
    `${h.slots} slot${h.slots === 1 ? '' : 's'}`,
    `${gib(h.memoryMb)} GiB`,
    h.since ? `held ${age((now - h.since) / 1000)}` : '',
    h.dir ? `in ${checkout(h.dir)}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

// A Bash command that runs magus: `magus`, `./magus` or a path ending in /magus, as a
// word of the command line.
export function runsMagus(command: string): boolean {
  return /(^|[\s;&|(])(\S*\/)?magus(\s|$)/.test(command)
}

// The refusal a magus run's output carries, by its diagnostic code; null when it has none.
export function refusalIn(command: string, output: string, at: number): Refusal | null {
  const line = output.split('\n').find(l => l.includes(REFUSED))
  if (line === undefined) return null
  return { at, command: command.trim().slice(0, 120), line: line.trim().slice(0, 300) }
}

// The newest refusal still inside the band's window, or null.
export function recentRefusal(refusals: Refusal[], now: number): Refusal | null {
  const r = refusals[0]
  return r !== undefined && now - r.at <= REFUSAL_SHOW_MS ? r : null
}

// The band's line for a refusal, with the budget as last read.
export function refusalText(r: Refusal, read: BudgetRead | null, now: number): string {
  const when = `${age((now - r.at) / 1000)} ago`
  const budget = read?.budget ? `: budget ${capacityText(read.budget)}` : ''
  return `MGS3009 refused \`${r.command}\` ${when}${budget}`
}
