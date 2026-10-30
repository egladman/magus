### Changed

- **A placed checkout starts with a symbol index.** `bootstrap-checkout` and
  `bootstrap-worktree` copy `.magus/symbols` from the checkout that placed them, so its first
  read updates an index instead of building one. A seed that fails prints a line and the
  placement carries on.
