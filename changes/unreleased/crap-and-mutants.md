### Added

- **A function a change touches is scored by CRAP and mutated.** Spells declare decision
  tokens, token swaps and a coverage command as data; `Magus.Crap` and `Magus.Mutants`
  answer for a change, and Buzz reads them as `magus\crap` and `magus\mutants`.
  `// magus:mutant op=<swap> reason=<why>` excepts an equivalent mutant
  ([ADR 0023](docs/decisions/0023-touched-functions-meet-crap-and-their-mutants.md)).

### Fixed

- **A runner ref inside a command token resolves for `MAGUS` and `MAGUS_` names.**
  `--cov-report=lcov:$MAGUS_COVERAGE_DIR/lcov.info` expands; other `$NAME` tokens are left
  to the shell.
