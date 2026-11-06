### Added

- **Each touched function is judged against itself at base.** Its change risk is measured
  again in an export of the merge base, and a function is refused only when it is worse
  there too, so shrinking a function never costs.
