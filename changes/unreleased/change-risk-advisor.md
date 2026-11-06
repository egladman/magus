### Added

- **A pull request advisor names the riskiest touched functions.** `change-risk` lists
  the functions a change touches by change-risk score, then those with no coverage profile
  and why, as measurements and no verdict. It reads the checks the diff already holds and
  runs no test. `magus diff` runs it locally.
