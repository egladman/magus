### Added

- **`security` tells a change's vulnerabilities from the base's.** Each actionable
  govulncheck or pnpm audit finding is labeled introduced or inherited against the vcs
  base ref. On a pull request an inherited finding prints `[inherited]` and passes. A
  merge queue candidate passes `--inherited=fatal` to fail on inherited findings too;
  the Security scanning guide shows the recipe.
