### Added

- **A job's footprint.** `magus job wait` names the declaration each changed line lands
  in, found with git's own funcname patterns and measured from the merge base, and
  `magus ls jobs` compares footprints when it reports overlapping write paths. Lines
  above a file's first declaration land in its `(preamble)`.
- **Diff drivers in the managed `.gitattributes` block**, with funcname patterns for
  TypeScript and Buzz. `magus doctor` reports a missing one.
