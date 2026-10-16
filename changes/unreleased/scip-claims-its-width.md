### Fixed

- **`magus graph build` no longer lets every worker's scip run take every core.** The root
  project's `scip` op now claims 4 slots and 2048 MiB, the width and peak measured over
  recent scip-go runs, so the broker seats two at once instead of all of them. A build whose
  indexer the full budget refuses (MGS3009) now fails the graph build, as a lock refusal
  (MGS3007) already did, instead of hiding the stale index behind `--silent`.
