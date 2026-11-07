import type { Budget, BudgetHolder, BudgetRead, BudgetRefusal, BudgetWaiter } from '../types'
import { MagusError, fail, isRecord, ok, parseJSON, type Result } from './result'
import { age } from './tree'

export type { Budget, BudgetHolder, BudgetRead, BudgetRefusal, BudgetWaiter }

// jsonv2 leaves zero values out, so an absent count is 0.
const int = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const bool = (v: unknown): boolean => v === true
const time = (v: unknown): number => (typeof v === 'string' ? Date.parse(v) || 0 : 0)
const records = (v: unknown): Record<string, unknown>[] => (Array.isArray(v) ? v.filter(isRecord) : [])

function holderOf(h: Record<string, unknown>): BudgetHolder {
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

function waiterOf(w: Record<string, unknown>): BudgetWaiter {
  return {
    pid: int(w.pid),
    project: str(w.project),
    target: str(w.target),
    dir: str(w.dir),
    command: str(w.command),
    slots: int(w.slots),
    memoryMb: int(w.memory_mb),
    owner: str(w.owner),
    tree: str(w.tree),
    since: time(w.since),
  }
}

function refusalOf(r: Record<string, unknown>): BudgetRefusal {
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

const statusError = (message: string): Result<never> => fail(new MagusError({ code: '', message }))

// The broker's build budget from `magus server status -o json`. budget is
// null when the report has no broker, which is a budget nothing holds.
export function parseBudget(stdout: string): Result<Budget | null> {
  const parsed = parseJSON(stdout)
  if (parsed.error)
    return statusError(`magus server status -o json printed no JSON${stdout ? `: ${stdout.slice(0, 120)}` : ''}`)
  const doc = parsed.value
  if (!isRecord(doc)) return statusError('magus server status -o json printed no object')
  if (doc.broker == null) return ok(null)
  if (!isRecord(doc.broker) || !isRecord(doc.broker.capacity)) {
    return statusError('magus server status -o json has a broker without broker.capacity')
  }
  const c = doc.broker.capacity
  return ok({
    budgetSlots: int(c.budget_slots),
    heldSlots: int(c.held_slots),
    budgetMb: int(c.budget_mb),
    heldMb: int(c.held_mb),
    holders: records(c.holders).map(holderOf),
    waiting: records(c.waiting).map(waiterOf),
    refused: records(c.refused).map(refusalOf),
  })
}

export function gib(mb: number): string {
  return (mb / 1024).toFixed(1)
}

export function capacityText(b: Budget): string {
  return `${b.heldSlots}/${b.budgetSlots} slots, ${gib(b.heldMb)}/${gib(b.budgetMb)} GiB`
}

// The last path segment: the checkout's name, which is what tells two worktrees apart.
export function checkout(dir: string): string {
  return dir.replace(/\/+$/, '').split('/').at(-1) ?? dir
}

const what = (project: string, target: string): string => (project && project !== '.' ? `${project} ${target}` : target)
const slotsText = (n: number): string => `${n} slot${n === 1 ? '' : 's'}`

export function holderText(h: BudgetHolder, now: number): string {
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

export function waiterText(w: BudgetWaiter, now: number): string {
  const why = w.tree ? `for ${w.tree}'s seat` : ''
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

// The Budget tab's line for a refusal.
export function refusalText(r: BudgetRefusal, now: number): string {
  const who = [r.owner, `pid ${r.pid}`, r.dir ? `in ${checkout(r.dir)}` : ''].filter(Boolean).join(', ')
  const never = r.fits ? '' : ' (never fits)'
  return `MGS3009 refused ${what(r.project, r.target)}${never} for ${who} ${age((now - r.at) / 1000)} ago`
}
