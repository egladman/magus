### Fixed

- **A killed `go test` no longer leaves the process-group tests' shell loops running.**
  Two `internal/proc/spawn` tests start loops only their group kill ended, so a killed
  run orphaned them and they forked thousands of processes a second. Each loop now runs
  only while the test process is alive.
