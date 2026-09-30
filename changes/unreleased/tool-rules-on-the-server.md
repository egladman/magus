### Changed

- **The toolchain view's violation flag and window text come from the server.** `ListTools`
  returns `violation`, `spell_window`, `workspace_window` and `effective_window` per tool,
  rendered by the code `magus describe tools` prints with, so the console and the CLI show
  the same windows. The console words the verdict, support and lifecycle enums from their
  value names, and `magus describe tools` prints a verdict as "too old" rather than
  `too_old`; the JSON output keeps `too_old`.
