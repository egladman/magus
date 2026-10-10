import type { RateTable } from './cost'

// The rates used when the ratesFile setting names none. Data only: a price change is an
// edit here, or a JSON file in this same shape named by the ratesFile setting, which wins.
// USD per million tokens; cache writes are input times cacheWriteMultiplier for the TTL.
export const DEFAULT_RATES: RateTable = {
  checked: '2026-10-06',
  source: 'Anthropic first-party API list prices; confirm at https://docs.claude.com/en/docs/about-claude/pricing',
  cacheWriteMultiplier: { '5m': 1.25, '1h': 2 },
  defaultCacheReadMultiplier: 0.1,
  models: [
    { match: 'claude-fable-5-1', input: 10, output: 50, cacheRead: 0.25 },
    { match: 'claude-opus-5-5', input: 4, output: 20, cacheRead: 0.2 },
    { match: 'claude-sonnet-5-5', input: 2, output: 10, cacheRead: 0.2 },
    { match: 'claude-haiku-5-5', input: 0.1, output: 0.5, tiers: [{ abovePromptTokens: 100_000, input: 0.5, output: 2.5 }] },
  ],
}
