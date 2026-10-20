### Added

- **Files that change together are linked in the knowledge graph.** With
  `knowledge.vcs.enabled`, the `@vcs` shard adds a symmetric `co_changes` edge between two
  files that share at least two commits and at least 30% of the less-changed file's
  commits, skipping commits that touch more than 30 files. Reproducible exports and the
  routing table leave these edges out.
