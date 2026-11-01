### Fixed

- **The spell cache restore finds the bundle filed under the previous UTC day after a local clock change.**
  The lookback stepped back in the caller's zone, so for an hour a day in the week after
  a daylight saving shift it skipped one day's key.
- **Token expiry dates and detached run logs are named in UTC.** `config mcp` and
  `doctor` print the same date for a token, and a detached log name sorts across a clock change.
