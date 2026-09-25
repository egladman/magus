### Added

- **The agent guard refuses an edit into another job's claimed declaration.** When the
  hook payload carries the replacement, the guard applies it in memory and places the
  changed lines as the job footprint does; `claimed-declaration` denies a leased edit
  landing in a declaration another live job claims. A whole-file write stays graded by path.
