### Fixed

- **A cache hit no longer restores a nested project's generated files.** An output glob
  such as `**/gen/mocks/*.go` stops at a nested project's directory unless rooted there,
  in the snapshot, replay, drift gate and `--race=replay` alike. MGS3001 now judges a
  directly selected step's replay, catches a file replaced with its mtime kept, and
  names the likely writer.
