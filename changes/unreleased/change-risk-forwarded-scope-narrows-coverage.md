### Fixed

- **A forwarded test scope narrows a coverage run.** The coverage op takes its test op's
  `test_scope`, so `magus run go::coverage . -- ./internal/risk` no longer runs `./...`,
  and a buzz coverage scope no longer expands every `**/*.buzz`.
