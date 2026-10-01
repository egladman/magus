### Fixed

- **`magus doctor` fails when the hook interpreter is another build.** `guard-binary`
  runs `version` on the magus a hook would run and fails, naming both binaries, when
  it differs from the doctor's own or prints none.
