### Fixed

- **A runner ref inside a command token resolves for `MAGUS` and `MAGUS_` names.**
  `--output=$MAGUS_SYMBOL_INDEX.scip` expands; other `$NAME` tokens are left to the shell.
