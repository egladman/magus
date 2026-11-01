### Fixed

- **The guard refuses a commit recording a conflict marker and stops locking a landing out.**
  An integrating job may edit what its branches changed, a `./magus` newer than the tree is
  no longer called older, a worker's checkout has one bootstrap line that always runs, and a
  Buzz job gets a script check it can run.
