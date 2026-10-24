### Fixed

- **A guard verdict ref resolves from a sibling worktree.** `magus query output grd...` from a
  linked worktree now also reads the other linked worktrees whose store changed in the last
  day, newest first, so a verdict the hook stored in the checkout it judged in no longer
  fails MGS8001 in a worker's checkout.
