### Changed

- **The magus-multi-agent skill teaches the fork that `magus job fork` accepts.** Its
  examples claim files and file globs, never a directory, which fork refuses with
  MGS3018. It shows `<file>#<declaration>` as the way two jobs share a file, the
  `--check` form, and the order: fork the row, then spawn with the description
  `<parent>/<role> <job>`. Its description now asks to be loaded before the first
  subagent spawn, and rules the guard already enforces are cut to the line a refused
  worker needs.
