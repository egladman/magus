### Fixed

- **A killed `go test` no longer leaves the process-group tests' shell loops running.**
  Two tests in `internal/proc/spawn` start shell loops that only their group kill ended,
  so a test run killed in between orphaned them, and they forked thousands of processes a
  second until the machine could no longer look up users. Each loop now runs only while
  the test process is alive, and exits on its next pass once it is gone.
