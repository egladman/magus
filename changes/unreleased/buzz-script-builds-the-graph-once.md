### Changed

- **A `magus buzz` script builds the knowledge graph once.** Graph reads such as
  `magus\refs` share one build per script run, as a spell's already did, instead of
  rebuilding the symbol graph on every call.
