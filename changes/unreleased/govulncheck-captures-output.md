### Added

- **The go spell's `govulncheck` op captures its output and defaults `./...`.** Explicit
  args replace the default, so `{"args": ["-format", "json", "./..."]}` returns the
  JSON stream in `stdout`.
