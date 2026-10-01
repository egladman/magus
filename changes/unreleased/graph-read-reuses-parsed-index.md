### Changed

- **A domain read parses no SCIP index.** A parsed index is reused until its file changes,
  and a symbol read no longer rebuilds the agent session overlay every time the session
  store grows.
