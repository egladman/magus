### Added

- **`security` tells a change's vulnerabilities from the base's.** Each actionable
  govulncheck or pnpm audit finding is labeled introduced or inherited against the
  manifest at the vcs base ref. On a pull request an inherited finding prints
  `[inherited]` and passes; where the base is the head, it fails as before. The Security
  scanning guide shows how to copy the recipe.
