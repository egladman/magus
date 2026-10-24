### Changed

- **`bootstrap-checkout --gate-ready` builds the proofread binary.** It runs
  `magus run proofread-build libs/proofread` after the graph and symbol digest, so a fresh
  checkout passes the guard's pull-request-text test. A failed build stops it, naming the command.
