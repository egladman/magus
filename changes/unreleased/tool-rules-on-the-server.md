### Changed

- **The toolchain's violation flag and window text come from the server.** `ListTools`
  returns `violation` and the spell, workspace and effective windows per tool, rendered by
  the same code as `magus describe tools`, so the console and the CLI agree. The CLI text
  table prints a verdict as "too old"; JSON keeps `too_old`.
