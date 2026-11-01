### Fixed

- **The guard stops locking a landing out, and refuses a commit recording a conflict marker.**
  The new `conflict-commit` rule reads what a commit adds through `git diff --check`. An
  integrating job may edit what its branches changed, a `./magus` newer than the tree is no
  longer called older, and a worker's own checkout has one bootstrap line that always runs. A
  Buzz job gets a script check it can run, and MGS3040 offers one in place of `go-vet`.
