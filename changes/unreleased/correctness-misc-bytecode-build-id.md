### Changed

- **Compiled guard rules survive a rebuild of the same source.** The bytecode cache is
  keyed on the binary's Go build ID, not its file's mtime and size, so a fresh CI build
  of an unchanged commit reuses the last run's chunks.
