### Fixed

- **`magus doctor` no longer calls a slow hook interpreter hung.** The `guard-binary` check
  waits for `<interpreter> version` as long as a hook would, 10 seconds, where it gave up
  after 3 and failed an interpreter that answered in time on a loaded machine.
