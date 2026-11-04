### Removed

- **The repository's `commit-msg` git hook is gone.** The guard already judges an
  agent's commit subject on the base branch, so a person's own commit no longer
  runs magus. `git-hooks-install` is removed; run `git-hooks-remove:rw` once to
  delete the old shim.
