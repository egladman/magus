### Changed

- **A script evaluation builds one symbol graph.** Inside a scheduled attempt, `magus\graph().refs` reads the all-symbols graph instead of merging shards per symbol, and the attempt's compute-once holder ends with it, so nothing it built outlives the attempt. A request after the end computes again and says so once.
