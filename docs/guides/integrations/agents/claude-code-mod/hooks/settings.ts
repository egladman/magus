import type { PluginOptions } from 'claude-code'

import type { Ttl } from '../types'

export type Gate = 'off' | 'warn' | 'confirm'
export type SpawnGate = 'off' | 'warn' | 'deny'

export type Settings = {
  cacheGate: Gate
  cacheWarnMs: number
  gateMinTokens: number
  cacheTtl: 'auto' | Ttl
  priceStepGate: Gate
  priceStepMarginTokens: number
  spawnGate: SpawnGate
  spawnMaxInputUsdPerMTok: number
  sessionBudgetUsd: number
  budgetGate: Gate
  subagentTokenBudget: number
  ratesFile: string
  ratesStaleDays: number
}

const num = (v: unknown, fallback: number): number => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : fallback)

// A confirm or deny that is misspelled reads as the default rather than as off, so a typo
// never silently drops a guard.
const pick = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T => (allowed.includes(v as T) ? (v as T) : fallback)

// The manifest's userConfig values with its defaults, so a value the engine did not fill
// still reads as the documented default rather than as zero.
export function readSettings(o: PluginOptions): Settings {
  const ttl = o.cacheTtl
  return {
    cacheGate: pick(o.cacheGate, ['off', 'warn', 'confirm'], 'warn'),
    cacheWarnMs: num(o.cacheWarnMinutes, 2) * 60_000,
    gateMinTokens: num(o.gateMinTokens, 50_000),
    cacheTtl: ttl === '5m' || ttl === '1h' ? ttl : 'auto',
    priceStepGate: pick(o.priceStepGate, ['off', 'warn', 'confirm'], 'warn'),
    priceStepMarginTokens: num(o.priceStepMarginTokens, 10_000),
    spawnGate: pick(o.spawnGate, ['off', 'warn', 'deny'], 'warn'),
    spawnMaxInputUsdPerMTok: num(o.spawnMaxInputUsdPerMTok, 2),
    sessionBudgetUsd: num(o.sessionBudgetUsd, 0),
    budgetGate: pick(o.budgetGate, ['off', 'warn', 'confirm'], 'warn'),
    subagentTokenBudget: num(o.subagentTokenBudget, 100_000),
    ratesFile: typeof o.ratesFile === 'string' ? o.ratesFile.trim() : '',
    ratesStaleDays: num(o.ratesStaleDays, 60),
  }
}
