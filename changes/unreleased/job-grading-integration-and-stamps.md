### Fixed

- **`magus job wait` grades an integration job.** Its merge range is not graded against
  its write paths, the go-test and lint runs its integrator files count as evidence, and
  a branch already on the base, or named by an integration job, counts as integrated.
