### Fixed

- **The babysit rebase line passes `--push`.** `hack/ci/pull-requests.buzz` emits
  `rebase-branches-onto-base` for a pull request whose base squash-merged. Since #816 the
  script pushes only with `--push`, so the line without it rebased and never published.
