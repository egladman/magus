### Fixed

- **`magus job fork` and the daemon's job tool write as the running build.** Their stores
  dropped the build's commit stamp, so every write was refused with MGS3051 even when that
  build had last written the job store.
- **`magus job wait` grades an integration job.** Its change is the merge range, so its
  changed paths are not graded against its write paths, and the go-test and lint runs its
  integrator files count as evidence for goals it holds.
- **A run is no longer stale because a generated output changed after it.** Paths magus
  maintains itself, and declared outputs where the store knows the workspace, are left out
  of the comparison.
- **Finished branches already on the base are not counted as waiting.** A branch whose
  changed files read the same on the base, as after a squash merge, a branch any
  integration job names, and a passed integration job's own branch count as integrated.
