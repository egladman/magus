### Changed

- **A skip_cache composer's members claim machine memory for themselves.** `ci` no
  longer sums its call's members into one claim, so members that each fit wait their
  turn. A member that can never fit is refused by name. `magus doctor` gains `gate-fits`,
  which fails when any ci step exceeds this machine's budget.
