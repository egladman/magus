import { describe, expect, test } from 'claude-code/testing'

import {
  budgetNotice,
  cacheNotice,
  cacheStatus,
  formatDuration,
  formatTokens,
  formatUsd,
  frontmatterModel,
  inferTtl,
  overSpawnCap,
  parseRates,
  priceStepNotice,
  pricedModel,
  rateAgeDays,
  ratesFor,
  recacheUsd,
  requestUsd,
  sessionUsd,
  spawnRefusal,
  spawnWarning,
  spendStatus,
  TTL_MS,
} from '../hooks/cost'
import type { CacheState, Spend } from '../hooks/cost'
import { DEFAULT_RATES } from '../hooks/rates'
import { readSettings } from '../hooks/settings'

const MIN = 60_000

// Float sums land a few ulps off the decimal price, so compare within a hair.
const near = (got: number | null | undefined, want: number) => expect(Math.abs((got ?? NaN) - want) < 1e-9).toBe(true)

describe('ratesFor', () => {
  test('the longest matching prefix wins and a [1m] suffix is ignored', async () => {
    const t = { ...DEFAULT_RATES, models: [...DEFAULT_RATES.models, { match: 'claude-opus', input: 99, output: 99 }] }
    expect(ratesFor(t, 'claude-opus-5-5[1m]', 1000)?.input).toBe(4)
    expect(ratesFor(t, 'claude-opus-4-8', 1000)?.input).toBe(99)
    expect(ratesFor(t, 'gpt-x', 1000)).toBeNull()
  })

  test('cache writes are input times the TTL multiplier; reads use the model rate or the default', async () => {
    const opus = ratesFor(DEFAULT_RATES, 'claude-opus-5-5', 1000)!
    expect(opus.cacheWrite).toEqual({ '5m': 5, '1h': 8 })
    expect(opus.cacheRead).toBe(0.2)
    near(ratesFor(DEFAULT_RATES, 'claude-haiku-5-5', 1000)!.cacheRead, 0.01)
  })

  test('Haiku switches tier above 100K prompt tokens', async () => {
    expect(ratesFor(DEFAULT_RATES, 'claude-haiku-5-5', 100_000)!.input).toBe(0.1)
    const above = ratesFor(DEFAULT_RATES, 'claude-haiku-5-5', 100_001)!
    expect([above.input, above.output]).toEqual([0.5, 2.5])
    near(above.cacheRead, 0.05)
  })
})

test('recacheUsd and requestUsd price tokens per million', async () => {
  near(recacheUsd(DEFAULT_RATES, 'claude-opus-5-5', 200_000, '1h'), 1.6)
  expect(recacheUsd(DEFAULT_RATES, 'unknown', 200_000, '1h')).toBeNull()
  const usd = requestUsd(DEFAULT_RATES, 'claude-sonnet-5-5', { input: 1000, output: 2000, cacheRead: 100_000, cacheCreation: 10_000 }, '5m')
  near(usd, (1000 * 2 + 2000 * 10 + 100_000 * 0.2 + 10_000 * 2.5) / 1e6)
})

describe('inferTtl', () => {
  const hit = { input: 10, output: 5, cacheRead: 150_000, cacheCreation: 200 }
  const miss = { input: 10, output: 5, cacheRead: 0, cacheCreation: 150_000 }
  test('a hit after more than 5 minutes means an hour; a miss inside the hour means 5 minutes', async () => {
    expect(inferTtl(20 * MIN, hit)).toBe('1h')
    expect(inferTtl(20 * MIN, miss)).toBe('5m')
  })
  test('a short gap, or a miss past the hour, settles nothing', async () => {
    expect(inferTtl(2 * MIN, hit)).toBeNull()
    expect(inferTtl(2 * MIN, miss)).toBeNull()
    expect(inferTtl(90 * MIN, miss)).toBeNull()
  })
})

describe('cacheNotice and cacheStatus', () => {
  const cache: CacheState = { lastResponseAt: 0, ttl: '5m', ttlSource: 'assumed', contextTokens: 182_000, model: 'claude-opus-5-5' }
  const at = (ms: number): CacheState => ({ ...cache, lastResponseAt: ms })

  test('nothing before the first response', async () => {
    expect(cacheNotice(cache, 1000, 5 * MIN, 50_000, DEFAULT_RATES)).toBeNull()
    expect(cacheStatus(cache, 1000)).toBeUndefined()
  })

  test('quiet while more than the window is left, a countdown inside it, expired after', async () => {
    const start = 1_000_000
    expect(cacheNotice({ ...at(start), ttl: '1h' }, start + MIN, 5 * MIN, 50_000, DEFAULT_RATES)).toBeNull()
    const soon = cacheNotice(at(start), start + 2 * MIN, 5 * MIN, 50_000, DEFAULT_RATES)!
    expect(soon.isExpired).toBe(false)
    expect(soon.text).toBe('Cache expires in 3m: after that your next prompt re-caches 182K tokens (≈$0.91) (TTL 5m, assumed).')
    const gone = cacheNotice(at(start), start + TTL_MS['5m'] + 4 * MIN, 5 * MIN, 50_000, DEFAULT_RATES)!
    expect(gone.isExpired).toBe(true)
    expect(gone.text).toContain('Cache expired 4m ago')
    expect(cacheStatus(at(start), start + 2 * MIN)).toBe('cache 3m')
    expect(cacheStatus(at(start), start + 6 * MIN)).toBe('cache expired')
  })

  test('a small context is not worth a warning', async () => {
    expect(cacheNotice({ ...at(1), contextTokens: 10_000 }, 10 * MIN, 5 * MIN, 50_000, DEFAULT_RATES)).toBeNull()
  })
})

describe('priceStepNotice', () => {
  const haiku = 'claude-haiku-5-5'

  test('quiet while the context is further under the step than the margin', async () => {
    expect(priceStepNotice(DEFAULT_RATES, haiku, 89_000, 10_000)).toBeNull()
  })

  test('names both rates and the room left inside the margin', async () => {
    const n = priceStepNotice(DEFAULT_RATES, haiku, 95_000, 10_000)!
    expect(n.isPast).toBe(false)
    expect(n.text).toBe(
      'Context is 95K tokens, 5K under the 100K step where claude-haiku-5-5 rises from $0.10 to $0.50 per MTok input and $0.50 to $2.50 output. Consider /compact before it crosses.',
    )
  })

  test('past the step, says what every request now pays', async () => {
    const n = priceStepNotice(DEFAULT_RATES, haiku, 130_000, 10_000)!
    expect(n.isPast).toBe(true)
    expect(n.text).toBe(
      'Context is 130K tokens, past the 100K step: every request on claude-haiku-5-5 now pays $0.50 per MTok input and $2.50 output, up from $0.10 and $0.50. Consider /compact.',
    )
  })

  test('the step itself is not past it, as the rates table prices it', async () => {
    expect(priceStepNotice(DEFAULT_RATES, haiku, 100_000, 0)).toBeNull()
    expect(priceStepNotice(DEFAULT_RATES, haiku, 100_000, 1)!.isPast).toBe(false)
  })

  test('a model without tiers, or an unpriced one, has no step', async () => {
    expect(priceStepNotice(DEFAULT_RATES, 'claude-opus-5-5', 900_000, 10_000)).toBeNull()
    expect(priceStepNotice(DEFAULT_RATES, 'gpt-x', 900_000, 10_000)).toBeNull()
  })

  test('the highest step in reach is the one named', async () => {
    const t = {
      ...DEFAULT_RATES,
      models: [{ match: 'm', input: 1, output: 2, tiers: [{ abovePromptTokens: 100_000, input: 2, output: 4 }, { abovePromptTokens: 500_000, input: 3, output: 6 }] }],
    }
    expect(priceStepNotice(t, 'm', 495_000, 10_000)!.text).toContain('under the 500K step where m rises from $2.00 to $3.00')
    expect(priceStepNotice(t, 'm', 150_000, 10_000)!.text).toContain('past the 100K step')
  })
})

describe('the subagent cost cap', () => {
  test('aliases and inherit resolve through the table, never a built-in price', async () => {
    expect(pricedModel(DEFAULT_RATES, 'haiku', 'claude-opus-5-5')).toBe('claude-haiku-5-5')
    expect(pricedModel(DEFAULT_RATES, 'Opus[1m]', 'claude-sonnet-5-5')).toBe('claude-opus-5-5')
    expect(pricedModel(DEFAULT_RATES, 'inherit', 'claude-sonnet-5-5')).toBe('claude-sonnet-5-5')
    expect(pricedModel(DEFAULT_RATES, 'claude-sonnet-5-5', 'x')).toBe('claude-sonnet-5-5')
    expect(pricedModel(DEFAULT_RATES, 'gpt-x', 'claude-opus-5-5')).toBeNull()
    expect(pricedModel(DEFAULT_RATES, 'inherit', 'gpt-x')).toBeNull()
  })

  test('anything pricier than the cap trips it; the cap itself, cheaper, 0 and unpriced do not', async () => {
    expect(overSpawnCap(DEFAULT_RATES, 'claude-opus-5-5', 2)).toEqual({ model: 'claude-opus-5-5', input: 4 })
    expect(overSpawnCap(DEFAULT_RATES, 'claude-fable-5-1', 2)).toEqual({ model: 'claude-fable-5-1', input: 10 })
    expect(overSpawnCap(DEFAULT_RATES, 'claude-sonnet-5-5', 2)).toBeNull()
    expect(overSpawnCap(DEFAULT_RATES, 'claude-haiku-5-5', 2)).toBeNull()
    expect(overSpawnCap(DEFAULT_RATES, 'claude-opus-5-5', 0)).toBeNull()
    expect(overSpawnCap(DEFAULT_RATES, 'gpt-x', 2)).toBeNull()
  })

  test('the refusal names the cheaper route, and says a fork cannot take model: haiku', async () => {
    const c = { model: 'claude-opus-5-5', input: 4 }
    expect(spawnWarning(c, 2)).toBe('Subagent will run on claude-opus-5-5 at $4.00 per MTok input, over your $2.00 cap.')
    expect(spawnRefusal(c, 2, false)).toContain('magus-scout agent for a lookup or proof, or pass model: haiku.')
    expect(spawnRefusal(c, 2, true)).toContain('A fork always inherits the session model')
  })

  test('reads the model line of an agent definition', async () => {
    expect(frontmatterModel('---\nname: "magus-scout"\nmodel: haiku\n---\n\nbody\nmodel: opus')).toBe('haiku')
    expect(frontmatterModel('---\nmodel: "claude-opus-5-5"\n---\n')).toBe('claude-opus-5-5')
    expect(frontmatterModel('---\nname: x\n---\nmodel: opus')).toBeUndefined()
    expect(frontmatterModel('no frontmatter\nmodel: opus')).toBeUndefined()
  })
})

describe('session spend', () => {
  const spend = (mainUsd: number, agentUsd: number, isPartial = false): Spend => ({ mainUsd, agentUsd, isPartial })

  test('the status shows the total, against the budget when there is one, and a floor when partial', async () => {
    expect(spendStatus(spend(0, 0), 5)).toBeUndefined()
    expect(sessionUsd(spend(1.5, 0.25))).toBe(1.75)
    expect(spendStatus(spend(1.5, 0.25), 0)).toBe('$1.75')
    expect(spendStatus(spend(1.5, 0.25), 5)).toBe('$1.75/$5.00')
    expect(spendStatus(spend(1.5, 0.25, true), 0)).toBe('$1.75+')
    expect(spendStatus(spend(0, 0, true), 0)).toBe('$0.00+')
  })

  test('the budget notice appears only once the total passes it', async () => {
    expect(budgetNotice(spend(4, 1), 5)).toBeNull()
    expect(budgetNotice(spend(4, 1), 0)).toBeNull()
    expect(budgetNotice(spend(4, 1.5), 5)).toBe('Session spend $5.50 has passed your $5.00 budget (main $4.00, subagents $1.50).')
  })
})

describe('parseRates', () => {
  test('reads the default shape', async () => {
    expect(parseRates(JSON.stringify(DEFAULT_RATES)).models).toHaveLength(4)
  })
  test('names the field it could not use', async () => {
    const bad = { ...DEFAULT_RATES, models: [{ match: 'claude-x', input: 'four', output: 20 }] }
    expect(() => parseRates(JSON.stringify(bad))).toThrow('models[0] (claude-x) needs numeric input and output')
    expect(() => parseRates('{"models":[]}')).toThrow('cacheWriteMultiplier')
  })
})

test('formatting and the rates age', async () => {
  expect(formatTokens(182_400)).toBe('182K')
  expect(formatTokens(1_250_000)).toBe('1.3M')
  expect(formatUsd(0.004)).toBe('<$0.01')
  expect(formatUsd(1.6)).toBe('$1.60')
  expect(formatDuration(45_000)).toBe('45s')
  expect(formatDuration(65 * MIN)).toBe('1h5m')
  expect(rateAgeDays(DEFAULT_RATES, Date.parse('2026-10-20'))).toBe(10)
})

test('readSettings fills the documented defaults and ignores values outside the options', async () => {
  expect(readSettings({})).toEqual({
    cacheGate: 'warn',
    cacheWarnMs: 2 * MIN,
    gateMinTokens: 50_000,
    cacheTtl: 'auto',
    priceStepGate: 'warn',
    priceStepMarginTokens: 10_000,
    spawnGate: 'warn',
    spawnMaxInputUsdPerMTok: 2,
    sessionBudgetUsd: 0,
    budgetGate: 'warn',
    subagentTokenBudget: 100_000,
    ratesFile: '',
    ratesStaleDays: 60,
  })
  expect(readSettings({ cacheGate: 'loud', cacheTtl: '1h', cacheWarnMinutes: 2 })).toMatchObject({ cacheGate: 'warn', cacheTtl: '1h', cacheWarnMs: 2 * MIN })
})

test('the new gates take their own words, and 0 is a value, not a missing one', async () => {
  expect(readSettings({ priceStepGate: 'confirm', spawnGate: 'deny', budgetGate: 'off', spawnMaxInputUsdPerMTok: 0 })).toMatchObject({
    priceStepGate: 'confirm',
    spawnGate: 'deny',
    budgetGate: 'off',
    spawnMaxInputUsdPerMTok: 0,
  })
  expect(readSettings({ spawnGate: 'confirm', priceStepGate: 'deny', sessionBudgetUsd: -3 })).toMatchObject({
    spawnGate: 'warn',
    priceStepGate: 'warn',
    sessionBudgetUsd: 0,
  })
})
