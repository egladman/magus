### Changed

- **A guard deny serves its remedy as `next`.** output-pipe, output-redirect, raw-tool,
  stage-all, process-poll, symbol-search and sibling-checkout put the command they
  computed from the refused line in a `next` field (id `deny-<rule>`) and under a
  one-line reason, so running it is pre-authorized and counted. A remedy is graded for
  the acting lease first and dropped when that role could not run it.
- **busy-wait says what it proves about a foreign process.** A sleep loop probing
  `kill -0`, the process table, `magus status` or a job row is told it holds a tool slot
  for its whole wait, not that the process will announce its end.
