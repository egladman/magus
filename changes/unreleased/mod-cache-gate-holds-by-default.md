### Changed

- **The Claude Code mod's cost gates hold the prompt by default.** `cacheGate`,
  `priceStepGate` and `budgetGate` default to `confirm`: a submit after the prompt
  cache expired, near a price step or past the budget is held once, its text put
  back, and the next submit sends it. Set a gate to `warn` for the toast alone.

### Fixed

- **The mod's cost gates speak once per response.** A prompt queued mid-turn after
  an expired-cache submit no longer raises the notice a second time.
