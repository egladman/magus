### Changed

- **Scratch copies of a repository live under `$XDG_CACHE_HOME/magus/checkouts`.** The merge
  queue, `graph diff --rev`, the release compat check, jj and hg exports and
  `merge-job-branches.buzz` use `<purpose>/<id>` there, so one mise trust entry covers
  them. Buzz makes one with `fs\scratchCheckoutDir(purpose)`.
