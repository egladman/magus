### Changed

- **A missing or stale symbol index says why.** `magus refs` and the guard's stale-graph
  advice name the cause they can observe, with its remedy: no server running, a refresh
  hook whose binary (such as `./magus`) is missing, no refresh hook installed, a server on
  another build, or a sync running now. `magus job run sync-graph` records what each
  request did.
