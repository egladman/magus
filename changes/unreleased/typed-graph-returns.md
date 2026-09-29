### Changed

- **`magus\query`, `explain`, `path`, `refs`, `stats`, and `output` return typed objects.** Annotate them `> QueryResult`, `> ExplainResult`, `> PathResult`, `> RefsResult`, `> KnowledgeStats`, and `> OutputRecord`.
- **Breaking: their keys are camelCase.** `node_count` is `nodeCount`, `match_count` is `matchCount`, `schema_version` is `schemaVersion`, `file_count` and `ref_count` are `fileCount` and `refCount`, `blast_radius` is `blastRadius`, `docs_url` is `docsURL`, and `duration_ms` is `durationMs`. An old key reads as null rather than raising, so a script that read one needs the new name.
- **Breaking: `magus\explain(node, to:)` is gone.** A path between two nodes is `magus\path(node, to:)`.
