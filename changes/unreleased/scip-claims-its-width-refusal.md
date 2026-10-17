### Fixed

- **`magus graph build` fails when the build budget refuses its indexer.** A refusal
  (MGS3009) now fails the graph build, as a lock refusal (MGS3007) already did, instead of
  hiding the stale index behind `--silent`.
