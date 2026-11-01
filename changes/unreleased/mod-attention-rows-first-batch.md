### Changed

- **The Claude Code mod draws less above the prompt.** The band keeps a guard that fails
  open and a daemon on another version; a stopped daemon and an MCP endpoint that is not
  serving move to the status line, and a stale or borrowed `./magus` is no longer drawn.
  The cache countdown, the MGS3009 refusal and its holders, Recheck, Hide, the rates age
  line and the spend, budget and cache status entries are gone. The Budget tab keeps the
  refusals.
- **The `spawnGate` setting is `off` or `deny`, and defaults to `off`.** The warn value and
  the toast it raised are removed; a stored `warn` reads as `off`. The
  `subagentTokenBudget`, `cacheWarnMinutes` and `ratesStaleDays` settings are removed with
  the toast and the "over budget" tag they fed.

### Fixed

- **The mod's Copy fix button says when nothing was copied.** It read success from the
  call and never checked it, so a remote client or a missing clipboard still toasted
  `Copied`. It now reports the reason and the command to run by hand.
