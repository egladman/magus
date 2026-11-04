### Fixed

- **A runner ref inside a command token resolves for `MAGUS` and `MAGUS_` names.**
  `--cov-report=lcov:$MAGUS_COVERAGE_DIR/lcov.info` expands; other `$NAME` tokens are left
  to the shell.
- **`Change.risk()` no longer deadlocks under `lint-rules` or needs the whole suite
  green.** Coverage runs in a scratch copy, scoped to the touched files' tests; a red test
  in scope makes its functions gaps that name the command.
- **A mutant that does not compile or load is killed.** When its baseline passed in the
  same copy, a mutant run with no result counts as killed instead of ending the run.
