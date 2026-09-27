### Changed

- **A guard deny serves its remedy as `next`.** output-pipe, output-redirect, raw-tool,
  stage-all, process-poll, symbol-search and sibling-checkout put the command they
  computed from the refused line in a `next` field (id `deny-<rule>`) and under a
  reason, so running it is pre-authorized and counted. A remedy is graded for
  the acting lease and dropped when that role could not run it.
