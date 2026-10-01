### Added

- **`magus job wait --integration` grades a job's check goals in another tree.** The
  `--stdin` result names runs recorded in the caller's checkout, such as a merged
  integration tree. The grade is recorded as the job's `integration`, beside its state,
  which does not move, and an ended job is graded too.
