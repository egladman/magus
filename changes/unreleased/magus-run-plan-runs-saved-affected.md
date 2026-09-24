### Added

- **`magus run --plan <file|->` runs a saved `affected --plan`; `--shard <id>` runs one
  shard.** The plan now carries a `target` key. A malformed plan, an unknown shard, another
  target or a disagreeing `--n-shards` exits 2 (MGS3026). Under `--dry-run` it loads
  nothing and renders the plan through `-o`. Without `--plan`, `--shard` stays a label.
