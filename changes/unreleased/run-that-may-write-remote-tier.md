### Added

- **A run that may write the remote tier backfills it.** A local hit whose key the remote
  tier lacks is uploaded in the background, after a `has_artifact` lookup; the cache
  contract gains that optional function.
