### Fixed

- **Dates that name a day are in UTC.** The spell cache restore finds the bundle filed under
  the previous UTC day after a local clock change, where it used to skip that key for an
  hour a day. Token expiry in `config mcp` and `doctor`, and detached log names, use UTC too.
