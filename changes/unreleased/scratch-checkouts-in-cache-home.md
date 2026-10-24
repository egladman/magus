### Changed

- **Scratch copies of a repository live under `$XDG_CACHE_HOME/magus/checkouts`.** The
  merge queue, `graph diff --rev`, the release compat check, jj and hg exports, and
  `merge-job-branches.buzz` each make theirs in `magus/checkouts/<purpose>/<id>` in the
  cache instead of the OS temp dir. One `mise settings add trusted_config_paths
  ~/.cache/magus/checkouts` trusts them all, and `merge-job-branches.buzz` names that
  command when mise refuses its tree. Buzz makes one with `fs\scratchCheckoutDir(purpose)`.
