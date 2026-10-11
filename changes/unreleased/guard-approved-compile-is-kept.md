### Fixed

- **A slow compile of the committed guard policy costs one hook, not every hook.** While a
  policy change is uncommitted, the guard keeps its compile of the committed sources,
  keyed by the committed bytes, where no working-tree load writes. A compile that runs
  past its share of the hook's deadline still finishes and is kept for the next hook.
