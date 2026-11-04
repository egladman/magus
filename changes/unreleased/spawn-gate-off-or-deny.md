### Changed

- **The `spawnGate` setting is `off` or `deny`, and defaults to `off`.** The warn value and
  the toast it raised are removed; a stored `warn` reads as `off`. The
  `subagentTokenBudget`, `cacheWarnMinutes` and `ratesStaleDays` settings are removed with
  the toast and the "over budget" tag they fed.
