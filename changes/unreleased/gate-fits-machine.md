### Changed

- **A skip_cache composer's members claim machine memory for themselves.** `ci` no
  longer sums its members into one claim, and a step waiting on its ctx.needs lends
  its claim back for the wait. A member that can never fit is refused by name, and
  `magus doctor` gains `gate-fits` for a ci step past this machine's budget.
