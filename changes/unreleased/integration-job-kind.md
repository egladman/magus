### Added

- **An integration job merges finished branches, and magus counts the ones waiting.** A
  fork record naming `integrates` computes its write paths from those branches, notes
  live holders instead of refusing, and lets its worker merge them.
  `magus session --brief` and other forks count finished branches off the base.
