### Fixed

- **A runner ref inside a command token resolves for `MAGUS` and `MAGUS_` names.**
  `--cov-report=lcov:$MAGUS_COVERAGE_DIR/lcov.info` expands; other `$NAME` tokens are left
  to the shell.
- **`Change.risk()` no longer deadlocks under `lint-rules` or needs the whole suite
  green.** Coverage runs in a scratch copy, scoped to the touched files' tests; a red test
  in scope makes its functions gaps that name the command.
- **A mutant that does not compile or load is killed.** When its baseline passed in the
  same copy, a mutant run with no result counts as killed instead of ending the run.
- **A forwarded test scope narrows a coverage run.** The coverage op takes its test op's
  `test_scope`, so `magus run go::coverage . -- ./internal/risk` no longer runs `./...`,
  and a buzz coverage scope no longer expands every `**/*.buzz`.
- **`Change.risk()` makes one coverage run per project and spell at each side, head and
  base at once, and `Change.mutants()` one baseline per scope.** Each copy links every
  project's ignore dirs, such as `node_modules`.
- **`Change.risk()` and `Change.mutants()` stop at a 7 minute budget and say what they
  were running.** A stop names the command or mutant and the elapsed time, never a bare
  `context canceled`.
