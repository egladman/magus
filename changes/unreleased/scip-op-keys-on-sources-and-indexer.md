### Changed

- **The scip op keys on its language's sources and its indexer's version.** An install
  that adds `node_modules` no longer marks a symbol index out of date, and upgrading
  scip-go or scip-typescript now does. An indexing spell declares its indexer as an
  observed tool, so its version keys only the scip op.
