### Changed

- **Breaking: the typed graph returns use camelCase keys.** `node_count` is `nodeCount`,
  `match_count` is `matchCount`, `schema_version` is `schemaVersion`, `file_count` and
  `ref_count` are `fileCount` and `refCount`, `blast_radius` is `blastRadius`, `docs_url`
  is `docsURL`, and `duration_ms` is `durationMs`. An old key reads as null rather than
  raising, so a script that read one needs the new name.
