### Changed

- **The magus-multi-agent skill teaches the fork that `magus job fork` accepts.** Its
  examples claim files and file globs, never a directory, which fork refuses with MGS3018.
  It shows `<file>#<declaration>` for two jobs sharing a file, the `--check` form, and the
  order: fork the row, then spawn with the description `<parent>/<role> <job>`. Rules the
  guard enforces are cut.
