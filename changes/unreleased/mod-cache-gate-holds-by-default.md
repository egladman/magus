### Changed

- **The Claude Code mod's cache expiry gate holds the prompt by default.** `cacheGate`
  defaults to `confirm`: a submit after the prompt cache expired is held once, its
  text put back, and the next submit sends it. Set it to `warn` for the toast alone.

### Fixed

- **The mod's cost gates speak once per response.** A prompt queued mid-turn after
  an expired-cache submit no longer raises the notice a second time.
