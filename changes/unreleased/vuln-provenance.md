### Added

- **`security` tells a change's vulnerabilities from the base's.** Each actionable
  govulncheck or pnpm audit finding is labeled introduced or inherited against the
  manifest at the vcs base ref. On a pull request an inherited finding prints
  `[inherited]` and passes; where the base is the head, it fails as before. A merge
  queue candidate passes `--inherited=fatal` (`magus run ci -- --inherited=fatal`) to
  fail on inherited findings too. The Security scanning guide shows how to copy the
  recipe.
- **The go spell's `govulncheck` op captures its output and defaults `./...`.** Explicit
  args replace the default, so `{"args": ["-format", "json", "./..."]}` returns the
  JSON stream in `stdout`.
