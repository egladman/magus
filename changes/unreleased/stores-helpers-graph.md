### Fixed

- **Concurrent graph builds no longer leave the knowledge graph stale.** Two queries
  in one checkout could leave the manifest naming a shard the file on disk did not
  hold, and later builds never rewrote it.
