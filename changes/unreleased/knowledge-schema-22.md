### Changed

- **The knowledge schema is 22.** The `@vcs` shard carries `co_changes` edges, and the store
  keeps a lexical sidecar per domain and symbol shard plus `@lexical.idx`. A graph built at
  an earlier schema is read as absent and rebuilt in full on the next `magus graph build`.
  Nothing needs migrating.
