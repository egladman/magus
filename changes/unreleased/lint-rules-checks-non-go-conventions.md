### Changed

- **Repository file conventions run as `lint-rules`.** The checks on lockfiles, workflows,
  magusfiles, hook configs, skill declarations and the landing rotator moved out of a Go
  test into Buzz rules under `hack/lint/`, which the root `lint` target runs. Each
  finding names the file, the line, and the fix.
