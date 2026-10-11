import type { CacheState, Ttl } from '../types'

export type { CacheState, Ttl }

// USD per million tokens. cacheRead absent means the table's default multiplier of input.
export type Tier = { abovePromptTokens: number; input: number; output: number; cacheRead?: number }
export type ModelRates = { match: string; input: number; output: number; cacheRead?: number; tiers?: Tier[] }
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

// Reads a rates file, throwing a message that names the first field it could not use, so
// an edit that breaks the table says where rather than silently pricing at zero.
export function parseRates(text: string): RateTable {
  const t = JSON.parse(text) as Partial<RateTable>
  if (!t.cacheWriteMultiplier || !isNum(t.cacheWriteMultiplier['5m']) || !isNum(t.cacheWriteMultiplier['1h'])) {
    throw new Error('rates: cacheWriteMultiplier needs numeric "5m" and "1h"')
  }
  if (!isNum(t.defaultCacheReadMultiplier)) throw new Error('rates: defaultCacheReadMultiplier must be a number')
  if (!Array.isArray(t.models)) throw new Error('rates: models must be a list')
  t.models.forEach((m, i) => {
    if (typeof m.match !== 'string' || !m.match) throw new Error(`rates: models[${i}].match must name a model id prefix`)
    if (!isNum(m.input) || !isNum(m.output)) throw new Error(`rates: models[${i}] (${m.match}) needs numeric input and output`)
    m.tiers?.forEach((tier, j) => {
      if (!isNum(tier.abovePromptTokens) || !isNum(tier.input) || !isNum(tier.output)) {
        throw new Error(`rates: models[${i}].tiers[${j}] needs numeric abovePromptTokens, input and output`)
      }
    })
  })
  return { ...(t as RateTable), checked: String(t.checked ?? ''), source: String(t.source ?? '') }
}

// The rates for model at a prompt of promptTokens, or null when the table has no entry.
// The longest matching prefix wins, so `claude-opus-5-5` beats `claude-opus`, and a
// bracketed variant suffix (`[1m]`) is ignored.
export function ratesFor(table: RateTable, model: string, promptTokens: number): Rates | null {
  const id = model.replace(/\[.*\]$/, '')
  const entry = table.models
    .filter(m => id.startsWith(m.match))
    .sort((a, b) => b.match.length - a.match.length)[0]
  if (!entry) return null
  const tier = (entry.tiers ?? [])
    .filter(t => promptTokens > t.abovePromptTokens)
    .sort((a, b) => b.abovePromptTokens - a.abovePromptTokens)[0]
  const input = tier?.input ?? entry.input
  const output = tier?.output ?? entry.output
  const cacheRead = tier?.cacheRead ?? (tier ? undefined : entry.cacheRead) ?? input * table.defaultCacheReadMultiplier
  return {
    input,
    output,
    cacheRead,
    cacheWrite: { '5m': input * table.cacheWriteMultiplier['5m'], '1h': input * table.cacheWriteMultiplier['1h'] },
  }
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
  return (u.input * r.input + u.output * r.output + u.cacheRead * r.cacheRead + u.cacheCreation * r.cacheWrite[ttl]) / 1e6
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

// The cache's part of the status line; undefined before any response.
export function cacheStatus(cache: CacheState | null, now: number): string | undefined {
  const left = cacheRemainingMs(cache, now)
  if (left === null) return undefined
  return left > 0 ? `cache ${formatDuration(left)}` : 'cache expired'
}

export function totalTokens(u: Usage): number {
  return u.input + u.output + u.cacheRead + u.cacheCreation
}

export function rateAgeDays(table: RateTable, now: number): number | null {
  const t = Date.parse(table.checked)
  return Number.isFinite(t) ? Math.floor((now - t) / 86_400_000) : null
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
