### Added

- **`hack/dev/land-commits.buzz` and `hack/dev/update-stack.buzz` land and refresh branches.**
  `land-commits` cherry-picks commits onto a new branch from a fresh `origin/main`, runs
  CI's `pr-title`, `pr-description` and `pr-changelog` checks locally, then pushes and opens
  the pull request, stopping at the first failure. `update-stack` brings a stack of branches
  up to date in order with `magus vcs resolve --against`, and stops where a source file
  conflicts. Both take `--dry-run`.
