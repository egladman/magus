### Fixed

- **A shard that fails to assemble no longer fails graph reads.** `magus explain`, `query` and `refs` answer from the other shards and say what the answer lacks, such as a coverage profile that omits directories. `magus graph build` still writes the rest and exits nonzero, naming the shard.
