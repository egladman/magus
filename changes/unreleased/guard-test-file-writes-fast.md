### Fixed

- **An edit to a file in another job's soft paths no longer times out in the guard.** The
  guard checked every soft path of every live job against that job's diff; an integration
  job holding 526 test files ran each test-file edit past the 7s hook budget. It now checks
  only the entries covering the file being written.
