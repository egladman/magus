### Fixed

- **`--root` from another directory no longer loads that directory's modules.** A
  magusfile's imports resolve against its project, then the workspace root.
