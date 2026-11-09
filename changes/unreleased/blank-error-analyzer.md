### Added

- **`blankerror` flags a dropped error a change adds.** A changed function is compared with its
  merge base on `origin/main`. Where it gains `_ = f()` assignments of an error, those on changed
  lines are reported, unless a same-line comment gives the reason. Existing sites charge nobody.
