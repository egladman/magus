### Added

- **Lint flags a test's endless shell loop that runs in a process group of its own.** A
  go test that is cancelled runs no cleanup, so a `while :` loop started that way outlives
  it and forks until the machine hangs. The new `testloops` check reports `while :`,
  `while true`, `until false` and `for ((;;))` in a test that sets `Setpgid` or calls
  `SetupProcessGroup`, unless the loop checks `kill -0` on the test process.
