### Added

- **`blankerror` flags a dropped error a change adds.** A function with a changed line is
  compared with the same function at the merge base with `origin/main`. When it holds more
  `_ = f()` assignments of an error than it did, those on the changed lines are reported,
  unless a comment on the same line gives the reason. The sites already in the tree charge
  nobody.
