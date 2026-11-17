### Added

- **`GraphService.QueryNodes` ranks from a cursor.** `QueryNodesRequest.position` (path,
  offset, prefix and suffix) seeds the ranking with the defined symbols its prefix and
  suffix name. Nothing in the position's own file comes back, so the masked middle cannot.
  With a position, `query` takes field filters only.
- **A position query returns its `context`.** Each `DefinitionSite` is a match's doc comment
  and declaration line, kept only when its range verified, at most `budget` of them.
  `KnowledgeQueryOutput.context` holds the same sites.
- **A position the symbol index cannot vouch for is refused.** No probe, a gap or stale
  index in its project, or no file node answers `FAILED_PRECONDITION`, never empty.
- **`QueryNodesRequest.exclude_paths` drops paths before ranking.** No node at or under one
  is matched, seeds the walk, or is walked through.
