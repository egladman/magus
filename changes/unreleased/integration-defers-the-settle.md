### Changed

- **`magus job wait --integration` refuses a checkout that owes a regeneration.** It names
  each owed target and `magus vcs resolve` instead of grading a stale tree. The
  integrator merges every branch, runs `magus vcs resolve` once and commits what it
  stages. The git guide drops the settle hooks and `magus.settle` deferral, which are gone.
