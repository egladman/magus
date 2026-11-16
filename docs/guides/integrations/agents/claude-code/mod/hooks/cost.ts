import type { CacheState, Spend, Ttl } from '../types'
import { MagusError, fail, isRecord, ok, parseJSON, type Result } from './result'

export type { CacheState, Spend, Ttl }

// USD per million tokens. cacheRead or cacheWrite absent means the table's
// default multiplier of input.
export type Tier = {
  abovePromptTokens: number
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: Record<Ttl, number>
}
export type ModelRates = {
  match: string
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: Record<Ttl, number>
  tiers?: Tier[]
}
export type RateTable = {
  checked: string
  source: string
  cacheWriteMultiplier: Record<Ttl, number>
  defaultCacheReadMultiplier: number
  models: ModelRates[]
}

// The rates one request pays, USD per million tokens, after model and tier are chosen.
export type Rates = { input: number; output: number; cacheRead: number; cacheWrite: Record<Ttl, number> }

export type Usage = { input: number; output: number; cacheRead: number; cacheCreation: number }

export const TTL_MS: Record<Ttl, number> = { '5m': 5 * 60_000, '1h': 60 * 60_000 }

const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0

// MODELS_FILE is where `magus agent harness install` writes the Claude Code
// harness's model rows: the claude-code spell's tools dir joined with
// types.ToolModelRowsFile. One JSON object per line.
export const MODELS_FILE = '.claude/magus/tools/models'

// The API's multipliers of input, for a row that leaves a cache price out.
const CACHE_WRITE_MULTIPLIER: Record<Ttl, number> = { '5m': 1.25, '1h': 2 }
const CACHE_READ_MULTIPLIER = 0.1

const modelsError = (message: string) => fail(new MagusError({ code: '', message: `${MODELS_FILE}: ${message}` }))

// parseModelRows reads magus's models file into a rate table. It fails on the
// first line it cannot use, naming it, so a broken file never prices at zero.
// A row's price_step and step_factor become one tier: past that many prompt
// tokens every rate is multiplied by the factor.
export function parseModelRows(text: string): Result<RateTable> {
  const models: ModelRates[] = []
  for (const [i, raw] of text.split('\n').entries()) {
    if (raw.trim() === '') continue
    const row = readRow(raw, i + 1)
    if (row.error) return row
    models.push(row.value)
  }
  return ok({
    checked: '',
    source: MODELS_FILE,
    cacheWriteMultiplier: CACHE_WRITE_MULTIPLIER,
    defaultCacheReadMultiplier: CACHE_READ_MULTIPLIER,
    models,
  })
}

function readRow(raw: string, line: number): Result<ModelRates> {
  const doc = parseJSON(raw)
  if (doc.error) return modelsError(`line ${line}: not JSON: ${doc.error.message}`)
  const r = doc.value
  if (!isRecord(r) || typeof r.id !== 'string' || r.id === '') return modelsError(`line ${line}: needs an id`)
  if (!isNum(r.input) || !isNum(r.output)) return modelsError(`line ${line} (${r.id}): needs numeric input and output`)
  for (const k of ['cache_read', 'cache_write_5m', 'cache_write_1h', 'price_step', 'step_factor']) {
    if (r[k] !== undefined && !isNum(r[k])) return modelsError(`line ${line} (${r.id}): ${k} must be a number`)
  }
  const num = (k: string): number | undefined => (isNum(r[k]) ? r[k] : undefined)
  const input = r.input
  const cacheRead = num('cache_read') ?? input * CACHE_READ_MULTIPLIER
  const cacheWrite = {
    '5m': num('cache_write_5m') ?? input * CACHE_WRITE_MULTIPLIER['5m'],
    '1h': num('cache_write_1h') ?? input * CACHE_WRITE_MULTIPLIER['1h'],
  }
  const model: ModelRates = { match: r.id, input, output: r.output, cacheRead, cacheWrite }
  const step = num('price_step') ?? 0
  const factor = num('step_factor') ?? 0
  if (step > 0 && factor > 0) {
    model.tiers = [
      {
        abovePromptTokens: step,
        input: input * factor,
        output: r.output * factor,
        cacheRead: cacheRead * factor,
        cacheWrite: { '5m': cacheWrite['5m'] * factor, '1h': cacheWrite['1h'] * factor },
      },
    ]
  }
  return ok(model)
}

// The table entry for model: the longest matching prefix wins, so `claude-opus-5-5` beats
// `claude-opus`, and a bracketed variant suffix (`[1m]`) is ignored.
function entryFor(table: RateTable, model: string): ModelRates | undefined {
  const id = model.replace(/\[.*\]$/, '')
  return table.models.filter(m => id.startsWith(m.match)).sort((a, b) => b.match.length - a.match.length)[0]
}

// The rates for model at a prompt of promptTokens, or null when the table has no entry.
export function ratesFor(table: RateTable, model: string, promptTokens: number): Rates | null {
  const entry = entryFor(table, model)
  if (!entry) return null
  const tier = (entry.tiers ?? [])
    .filter(t => promptTokens > t.abovePromptTokens)
    .sort((a, b) => b.abovePromptTokens - a.abovePromptTokens)[0]
  const input = tier?.input ?? entry.input
  const output = tier?.output ?? entry.output
  const cacheRead = tier?.cacheRead ?? (tier ? undefined : entry.cacheRead) ?? input * table.defaultCacheReadMultiplier
  const cacheWrite = tier?.cacheWrite ??
    (tier ? undefined : entry.cacheWrite) ?? {
      '5m': input * table.cacheWriteMultiplier['5m'],
      '1h': input * table.cacheWriteMultiplier['1h'],
    }
  return { input, output, cacheRead, cacheWrite }
}

// What re-caching tokens of context costs on model, or null when the model is not priced.
export function recacheUsd(table: RateTable, model: string, tokens: number, ttl: Ttl): number | null {
  const r = ratesFor(table, model, tokens)
  return r ? (tokens * r.cacheWrite[ttl]) / 1e6 : null
}

// What one request cost, priced at its own prompt size. Cache writes are priced at ttl
// because the API reports writes without saying which TTL each token took.
export function requestUsd(table: RateTable, model: string, u: Usage, ttl: Ttl): number | null {
  const prompt = u.input + u.cacheRead + u.cacheCreation
  const r = ratesFor(table, model, prompt)
  if (!r) return null
  return (
    (u.input * r.input + u.output * r.output + u.cacheRead * r.cacheRead + u.cacheCreation * r.cacheWrite[ttl]) / 1e6
  )
}

// Milliseconds left before the cache written by the last response expires; null before
// any response. The entry's clock starts when its request started, so measuring from the
// response's end overstates what is left by that request's generation time.
export function cacheRemainingMs(cache: CacheState | null, now: number): number | null {
  if (!cache || cache.lastResponseAt === 0) return null
  return cache.lastResponseAt + TTL_MS[cache.ttl] - now
}

// What one response's usage says about the TTL: a cache hit after a gap longer than 5
// minutes means the entry lives for an hour; a full rewrite after a gap of 5 to 60 minutes
// means it lived for 5. null when the response settles nothing.
export function inferTtl(gapMs: number, u: Usage): Ttl | null {
  const reread = u.cacheRead > 0 && u.cacheRead >= u.cacheCreation
  if (gapMs > TTL_MS['5m'] && reread) return '1h'
  if (gapMs > TTL_MS['5m'] && gapMs < TTL_MS['1h'] && u.cacheRead === 0 && u.cacheCreation > 0) return '5m'
  return null
}

export type CacheNotice = { isExpired: boolean; usd: number | null; text: string }

// The warning for the band and the gate: null while the cache has more than warnMs left,
// or the context is under minTokens, or nothing has answered yet.
export function cacheNotice(
  cache: CacheState | null,
  now: number,
  warnMs: number,
  minTokens: number,
  table: RateTable,
): CacheNotice | null {
  const left = cacheRemainingMs(cache, now)
  if (cache === null || left === null || cache.contextTokens < minTokens || left > warnMs) return null
  const usd = recacheUsd(table, cache.model, cache.contextTokens, cache.ttl)
  const cost = usd === null ? '' : ` (≈${formatUsd(usd)})`
  const tokens = formatTokens(cache.contextTokens)
  const ttl = `TTL ${cache.ttl}, ${cache.ttlSource}`
  const isExpired = left <= 0
  const text = isExpired
    ? `Cache expired ${formatDuration(-left)} ago: your next prompt re-caches ${tokens} tokens${cost} (${ttl}).`
    : `Cache expires in ${formatDuration(left)}: after that your next prompt re-caches ${tokens} tokens${cost} (${ttl}).`
  return { isExpired, usd, text }
}

export type StepNotice = { isPast: boolean; text: string }

// The price step the next request meets, or null while the context is more than marginTokens
// under every step of model's tiers, or the model has none. A request re-sends the whole
// context, so past a step every request pays the higher rate until the context shrinks.
export function priceStepNotice(
  table: RateTable,
  model: string,
  contextTokens: number,
  marginTokens: number,
): StepNotice | null {
  const tier = (entryFor(table, model)?.tiers ?? [])
    .filter(t => contextTokens > t.abovePromptTokens - marginTokens)
    .sort((a, b) => b.abovePromptTokens - a.abovePromptTokens)[0]
  if (!tier) return null
  const low = ratesFor(table, model, tier.abovePromptTokens)
  const high = ratesFor(table, model, tier.abovePromptTokens + 1)
  if (!low || !high) return null
  const step = formatTokens(tier.abovePromptTokens)
  const tokens = formatTokens(contextTokens)
  const isPast = contextTokens > tier.abovePromptTokens
  const text = isPast
    ? `Context is ${tokens} tokens, past the ${step} step: every request on ${model} now pays ${formatUsd(high.input)} per MTok input and ${formatUsd(high.output)} output, up from ${formatUsd(low.input)} and ${formatUsd(low.output)}. Consider /compact.`
    : `Context is ${tokens} tokens, ${formatTokens(tier.abovePromptTokens - contextTokens)} under the ${step} step where ${model} rises from ${formatUsd(low.input)} to ${formatUsd(high.input)} per MTok input and ${formatUsd(low.output)} to ${formatUsd(high.output)} output. Consider /compact before it crosses.`
  return { isPast, text }
}

// The table id a subagent's model setting prices as. `inherit` is the parent's model; an
// alias (`haiku`) is the table's first entry of that family, since the table is the only
// source of prices. null when the table prices nothing under it.
export function pricedModel(table: RateTable, model: string, parentModel: string): string | null {
  const m = model
    .replace(/\[.*\]$/, '')
    .trim()
    .toLowerCase()
  const id = m === '' || m === 'inherit' ? parentModel : m
  if (entryFor(table, id)) return id
  return table.models.find(e => e.match.split('-')[1] === id)?.match ?? null
}

// The `model:` value of a markdown agent definition's frontmatter, if it sets one.
export function frontmatterModel(text: string): string | undefined {
  const head = /^---\r?\n([\s\S]*?)\r?\n---/.exec(text)?.[1]
  const value = head && /^model:\s*(.+?)\s*$/m.exec(head)?.[1]
  return value ? value.replace(/^["']|["']$/g, '') : undefined
}

export type SpawnCost = { model: string; input: number }

// The spawn's model and its input rate when that rate is over capUsd per MTok. null when
// the cap is off, the model is unpriced, or the rate is within the cap.
export function overSpawnCap(table: RateTable, model: string, capUsd: number): SpawnCost | null {
  const r = capUsd > 0 ? ratesFor(table, model, 0) : null
  return r && r.input > capUsd ? { model, input: r.input } : null
}

export function spawnWarning(c: SpawnCost, capUsd: number): string {
  return `Subagent will run on ${c.model} at ${formatUsd(c.input)} per MTok input, over your ${formatUsd(capUsd)} cap.`
}

export function spawnRefusal(c: SpawnCost, capUsd: number, isFork: boolean): string {
  const route = isFork
    ? 'A fork always inherits the session model: use the read-only magus-scout agent for a lookup or proof, or a non-fork agent with model: haiku.'
    : 'Use the read-only magus-scout agent for a lookup or proof, or pass model: haiku.'
  return `${spawnWarning(c, capUsd)} ${route}`
}

// sessionUsd is Claude Code's own total where it keeps one, else the mod's
// sum priced from its rates table.
export const sessionUsd = (s: Spend): number => s.claudeUsd ?? s.mainUsd + s.agentUsd

// The mod's sum stands in, and a response ran on a model it does not price.
const isFloor = (s: Spend): boolean => (s.claudeUsd ?? null) === null && s.isPartial

// The session total, against the budget when there is one. undefined before
// anything is spent.
export function spendStatus(s: Spend, budgetUsd: number): string | undefined {
  if (sessionUsd(s) === 0 && !isFloor(s)) return undefined
  const total = `${formatUsd(sessionUsd(s))}${isFloor(s) ? '+' : ''}`
  return budgetUsd > 0 ? `${total}/${formatUsd(budgetUsd)}` : total
}

// costStatus is the status line's cost part, `≈$1.75 session · ≈$12.40 today`,
// marked `(magus prices)` where Claude Code reported no total. Every figure is
// an estimate at list price, never the bill.
export function costStatus(s: Spend, budgetUsd: number): string | undefined {
  const session = spendStatus(s, budgetUsd)
  if (session === undefined) return undefined
  const hasDay = s.dayUsd !== undefined && s.dayUsd !== null
  const day = hasDay ? ` · ≈${formatUsd(s.dayUsd ?? 0)} today` : ''
  const source = (s.claudeUsd ?? null) === null ? ' (magus prices)' : ''
  return `≈${session} session${day}${source}`
}

// The mod's own split; Claude Code reports no split by main and subagent.
export function spendBreakdown(s: Spend): string {
  return `main ${formatUsd(s.mainUsd)}, subagents ${formatUsd(s.agentUsd)} at magus's prices`
}

// dayKey names the store key holding one local calendar day's total.
export function dayKey(ms: number): string {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `spend-${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// costDelta is what a new reading of the session total adds since the last.
// A total that went down was started over by /clear, so all of it is new.
export function costDelta(previous: number | null, usd: number): number {
  if (previous === null || usd < previous) return usd
  return usd - previous
}

// The budget gate's warning; null while the budget is off or not yet passed.
export function budgetNotice(s: Spend, budgetUsd: number): string | null {
  if (budgetUsd <= 0 || sessionUsd(s) <= budgetUsd) return null
  return `Session spend ${formatUsd(sessionUsd(s))} has passed your ${formatUsd(budgetUsd)} budget (${spendBreakdown(s)}).`
}

export function totalTokens(u: Usage): number {
  return u.input + u.output + u.cacheRead + u.cacheCreation
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${Math.round(n / 1_000)}K`
  return String(n)
}

export function formatUsd(usd: number): string {
  if (usd > 0 && usd < 0.01) return '<$0.01'
  return `$${usd.toFixed(2)}`
}

export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  return `${Math.floor(s / 3600)}h${Math.floor((s % 3600) / 60)}m`
}
