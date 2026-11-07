### Changed

- **Shard IDs read as stage then letter: `0a`, `1a`, `1b`.** Stage zero's shards were
  `stage-zero-0` and the rest `0`, `1` or `001`. Past 26 shards a stage's IDs take two
  letters, `1aa` to `1zz`, so they still sort. `--shard` takes the new IDs.
- **The plan's job summary says what fell back, why, and what it costs.** The plan's
  `fallbacks` are objects with `what`, `why` and `effect` in place of one sentence each,
  and the summary lays them out as a table under its shard list.
