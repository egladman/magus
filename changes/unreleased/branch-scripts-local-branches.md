### Changed

- **The rebase and merge-base branch scripts accept local branches.** They read a branch
  origin lacks from its local ref and push it with `-u`. `merge-base-into-branches.buzz`
  merges the base into each branch independently (`--stack` chains them), works in the
  worktree holding the branch, and prints each failed git call's stderr.
