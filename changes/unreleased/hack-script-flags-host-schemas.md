### Changed

- **`hack/host-schemas.buzz` takes steps, not an environment variable.** Run
  `magus buzz hack/host-schemas.buzz -- verify | fetch`. It has no default step: a bare
  run prints the usage line and fails.
