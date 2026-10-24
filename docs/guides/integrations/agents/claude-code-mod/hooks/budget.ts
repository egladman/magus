import type { Budget, BudgetHolder, BudgetRead } from '../types'
import { age } from './tree'

export type { Budget, BudgetHolder, BudgetRead }

// A holder with who answers for it: a job lease, `spawned by <job>`, or unleased. Empty
// when the broker could not name the holder's pid.
export type OwnedHolder = BudgetHolder & { owner: string }

// A caller blocked on a seat now: an entry of broker.capacity.waiting.
export type Waiter = {
  pid: number
  project: string
  target: string
  dir: string
  command: string
  slots: number
  memoryMb: number
  owner: string
  // A `magus status --wait` asking for a seat on pid's behalf.
  reservation: boolean
  // The job tree whose validation seat it waits for; '' when it waits on the budget.
  tree: string
  // Unix milliseconds of the first ask the broker turned away.
  since: number
}

// An MGS3009 refusal the broker answered, for any session on this machine: an entry of
// broker.capacity.refused, with the holders that kept the claim out at that moment.
export type BrokerRefusal = {
  at: number
  pid: number
  project: string
  target: string
  dir: string
  command: string
  slots: number
  memoryMb: number
  owner: string
  // false when no state of the machine admits the claim, which no wait mends.
  fits: boolean
  holders: OwnedHolder[]
}

// The budget as this mod reads it. It is stored under the contract's Budget, so a read
// back from state is narrowed with isBoard.
export type Board = Budget & { holders: OwnedHolder[]; waiting: Waiter[]; refused: BrokerRefusal[] }

export type Parsed = { ok: true; budget: Board | null } | { ok: false; error: string }

// How long the band names a refusal after it happened.
export const REFUSAL_SHOW_MS = 5 * 60_000

// jsonv2 leaves zero values out, so an absent count is 0.
const int = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const bool = (v: unknown): boolean => v === true
const time = (v: unknown): number => (typeof v === 'string' ? Date.parse(v) || 0 : 0)
const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const records = (v: unknown): Record<string, unknown>[] => (Array.isArray(v) ? v.filter(isRecord) : [])

export function isBoard(b: Budget): b is Board {
  return 'waiting' in b && 'refused' in b
}

function holderOf(h: Record<string, unknown>): OwnedHolder {
  return {
    pid: int(h.pid),
    project: str(h.project),
    target: str(h.target),
    dir: str(h.dir),
    command: str(h.command),
    slots: int(h.slots),
    memoryMb: int(h.memory_mb),
    since: time(h.since),
    owner: str(h.owner),
  }
}

function waiterOf(w: Record<string, unknown>): Waiter {
  return {
    pid: int(w.pid),
    project: str(w.project),
    target: str(w.target),
    dir: str(w.dir),
    command: str(w.command),
    slots: int(w.slots),
    memoryMb: int(w.memory_mb),
    owner: str(w.owner),
    reservation: bool(w.reservation),
    tree: str(w.tree),
    since: time(w.since),
  }
}

function refusalOf(r: Record<string, unknown>): BrokerRefusal {
  return {
    at: time(r.at),
    pid: int(r.pid),
    project: str(r.project),
    target: str(r.target),
    dir: str(r.dir),
    command: str(r.command),
    slots: int(r.slots),
    memoryMb: int(r.memory_mb),
    owner: str(r.owner),
    fits: bool(r.fits),
    holders: records(r.holders).map(holderOf),
  }
}

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
  return {
    ok: true,
    budget: {
      budgetSlots: int(c.budget_slots),
      heldSlots: int(c.held_slots),
      budgetMb: int(c.budget_mb),
      heldMb: int(c.held_mb),
      holders: records(c.holders).map(holderOf),
      waiting: records(c.waiting).map(waiterOf),
      refused: records(c.refused).map(refusalOf),
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

const what = (project: string, target: string): string => (project && project !== '.' ? `${project} ${target}` : target)
const slotsText = (n: number): string => `${n} slot${n === 1 ? '' : 's'}`

export function holderText(h: OwnedHolder, now: number): string {
  return [
    `pid ${h.pid}`,
    what(h.project, h.target),
    h.owner,
    slotsText(h.slots),
    `${gib(h.memoryMb)} GiB`,
    h.since ? `held ${age((now - h.since) / 1000)}` : '',
    h.dir ? `in ${checkout(h.dir)}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

export function waiterText(w: Waiter, now: number): string {
  const why = w.reservation ? 'reserving' : w.tree ? `for ${w.tree}'s seat` : ''
  return [
    `pid ${w.pid}`,
    what(w.project, w.target),
    w.owner,
    slotsText(Math.max(w.slots, 1)),
    why,
    w.since ? `waiting ${age((now - w.since) / 1000)}` : '',
    w.dir ? `in ${checkout(w.dir)}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

// A Bash command that runs magus: `magus`, `./magus` or a path ending in /magus, as a
// word of the command line.
export function runsMagus(command: string): boolean {
  return /(^|[\s;&|(])(\S*\/)?magus(\s|$)/.test(command)
}

// The newest refusal still inside the band's window, or null.
export function recentRefusal(b: Board | null, now: number): BrokerRefusal | null {
  const r = b?.refused[0]
  return r !== undefined && now - r.at <= REFUSAL_SHOW_MS ? r : null
}

// The band's line for a refusal, with the budget as last read.
export function refusalText(r: BrokerRefusal, budget: Budget | null, now: number): string {
  const who = [r.owner, `pid ${r.pid}`, r.dir ? `in ${checkout(r.dir)}` : ''].filter(Boolean).join(', ')
  const never = r.fits ? '' : ' (never fits)'
  const tail = budget ? `: budget ${capacityText(budget)}` : ''
  return `MGS3009 refused ${what(r.project, r.target)}${never} for ${who} ${age((now - r.at) / 1000)} ago${tail}`
}
