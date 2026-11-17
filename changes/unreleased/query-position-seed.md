### Added

- **`GraphService.QueryNodes` ranks from a cursor.** `QueryNodesRequest.position` (path,
  offset, prefix and suffix) seeds the ranking with the defined symbols its prefix and
  suffix name. Nothing in the position's own file comes back, so the masked middle cannot.
  With a position, `query` takes field filters only.
- **A position query returns its `context`.** Each `DefinitionSite` is a match's doc comment
  and declaration line, kept only when its range verified, at most `budget` of them.
  `KnowledgeQueryOutput.context` holds the same sites.
- **A position the symbol index cannot vouch for is refused.** No probe, a gap in its
  project, an index stale for any file but the position's own, or no file node answers
  `FAILED_PRECONDITION`, never empty.
- **The server keeps the symbol-merged graph between requests** until the warm graph or
  the symbol index status changes, so a position query no longer merges every shard.
- **`QueryNodesRequest.exclude_paths` drops paths before ranking.** No node at or under one
  is matched, seeds the walk, or is walked through.
