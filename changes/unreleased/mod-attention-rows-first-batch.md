### Added

- **`magus run mod-test docs/guides/integrations/agents` runs the mod's tests.** It needs
  `claude` 2.1.308 or newer on `PATH`. `ci` does not compose it.

### Changed

- **The Claude Code mod draws less above the prompt.** A stopped daemon and an MCP endpoint
  that is not serving move to the status line, and a stale `./magus` is no longer drawn. The
  cache countdown, Recheck, Hide and the spend, budget and cache entries are gone.
- **The `spawnGate` setting is `off` or `deny`, and defaults to `off`.** A stored `warn`
  reads as `off`. The `subagentTokenBudget`, `cacheWarnMinutes` and `ratesStaleDays`
  settings are removed.

### Fixed

- **The mod's Copy fix button says when nothing was copied.** A remote client or a missing
  clipboard no longer toasts `Copied`; it reports the reason and the command to run by hand.
