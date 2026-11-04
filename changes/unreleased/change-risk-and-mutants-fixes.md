### Fixed

- **A runner ref inside a command token resolves for `MAGUS` and `MAGUS_` names.**
  `--cov-report=lcov:$MAGUS_COVERAGE_DIR/lcov.info` expands; other `$NAME` tokens are left
  to the shell.
