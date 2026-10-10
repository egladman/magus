import type { PluginOptions } from 'claude-code'

import type { Ttl } from '../types'

export type Gate = 'off' | 'warn' | 'confirm'

export type Settings = {
  cacheGate: Gate
  cacheWarnMs: number
  gateMinTokens: number
  cacheTtl: 'auto' | Ttl
  subagentTokenBudget: number
  ratesFile: string
  ratesStaleDays: number
}

const num = (v: unknown, fallback: number): number => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : fallback)

// The manifest's userConfig values with its defaults, so a value the engine did not fill
// still reads as the documented default rather than as zero.
export function readSettings(o: PluginOptions): Settings {
  const gate = o.cacheGate
  const ttl = o.cacheTtl
  return {
    cacheGate: gate === 'off' || gate === 'confirm' ? gate : 'warn',
    cacheWarnMs: num(o.cacheWarnMinutes, 2) * 60_000,
    gateMinTokens: num(o.gateMinTokens, 50_000),
    cacheTtl: ttl === '5m' || ttl === '1h' ? ttl : 'auto',
    subagentTokenBudget: num(o.subagentTokenBudget, 100_000),
    ratesFile: typeof o.ratesFile === 'string' ? o.ratesFile.trim() : '',
    ratesStaleDays: num(o.ratesStaleDays, 60),
  }
}
