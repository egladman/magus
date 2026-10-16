### Added

- **Lint flags a test's endless shell loop that runs in a process group of its own.** A
  cancelled go test runs no cleanup, so such a loop outlives it and forks until the
  machine hangs. `testloops` reports `while :`, `while true`, `until false` and
  `for ((;;))` under `Setpgid` or `SetupProcessGroup` unless the loop checks `kill -0` on
  the test process.
