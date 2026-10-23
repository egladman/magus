### Added

- **`hack/dev/land-commits.buzz` and `hack/dev/merge-base-into-branches.buzz` land and refresh branches.** `land-commits` cherry-picks commits onto a new branch from a fresh `origin/main`, runs CI's pull request checks locally, then pushes and opens the pull request. `merge-base-into-branches` brings a stack up to date in order with `magus vcs resolve --against`. Both take `--dry-run`.
