### Changed

- **Embedding search scores every document exactly, and the index format is 2.** The
  sign-code stage, `SearchOptions.Rescore`, and `Vector.Code` are gone. Search shards
  100k 256-dimensional documents across cores and returns identical hits at any core
  count. Format-1 indexes are refused and must be rebuilt.
