### Changed

- **Breaking: the magus-buzz-write skill is now magus-buzz-lang, rebuilt around
  examples.** It carries one reference script that checks and runs, a table of the
  mistakes TypeScript, Go and Python habits produce with the diagnostic each one shows,
  every built-in `str`, list and map method, and the check-fix-run loop. Its examples no
  longer call `main()` themselves: `magus buzz` already calls it, so they ran twice.
  `magus agent install` prunes the old directory.
