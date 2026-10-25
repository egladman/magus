### Changed

- **Footprint calls take a list, and `ctx.modifiesExistingFiles` is gone.** Write
  `ctx.readsFiles(["src/**"])`, `ctx.writesFiles(["gen/**"])`, and
  `ctx.writesFiles(["go.mod"], editsExistingSource: true)` for a file the target edits.
  An edited source must exist before the target runs, and one path declared both ways
  is MGS1053.
