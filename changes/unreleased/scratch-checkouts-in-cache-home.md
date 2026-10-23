### Changed

- **`merge-job-branches.buzz` checks out under `$XDG_CACHE_HOME/magus/checkouts`.** Its
  worktree moves from the OS temp dir to `magus/checkouts/merge-job-branches/<id>` in the
  cache, beside where `xdg.ScratchCheckoutDir` puts every scratch copy of a repository.
  One `mise settings add trusted_config_paths ~/.cache/magus/checkouts` trusts them all,
  and the script names that command when mise refuses the tree.
