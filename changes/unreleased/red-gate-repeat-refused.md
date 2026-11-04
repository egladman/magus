### Changed

- **A failed gate is not re-run over the same tree.** The guard refuses `magus affected
  ci` when the newest gate failed, the tree is unchanged, and a failing target has not
  passed alone. The refusal names each failing target's `magus run`.
