### Added

- **`magus refs <symbol> --definition` prints where a body starts and ends.** Each
  definition reads as `path:start-end`, checked against the file: `verified` when it
  predates its index, `changed` (exit 1) when the name left the start line. `--source`
  adds the lines. A missing end line is said, never guessed.
