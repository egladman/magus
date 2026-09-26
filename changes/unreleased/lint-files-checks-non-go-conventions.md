### Changed

- **Repository file conventions run as `lint-files`.** The checks on lockfiles, workflows,
  magusfiles, hook configs, skill declarations and the landing rotator moved out of a Go
  test into `cmd/magus-filelint`, which the root `lint` target runs. Each finding names
  the file, the line, and the fix.
