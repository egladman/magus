### Added

- **`magus run --stdin` runs a saved `affected --plan`; `--shard <id>` runs one shard.**
  The plan now carries a `target` key. A malformed plan, an unknown shard, another target
  or a disagreeing `--n-shards` exits 2 (MGS3026). Under `--dry-run` it runs nothing and
  renders the plan through `-o`. Without `--stdin`, `--shard` stays a label.
