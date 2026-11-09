### Added

- **Every command declares its effect.** `cmdline.Command` and `cmdline.Flag` carry an
  `Effect`: read, local-write, shared-write, run, stream or wait. A flag's effect replaces
  the command's when the flag is set, so `--dry-run` reads and `server status --watch`
  streams. `cmdline.Check` refuses a value outside `cmdline.Effects`, and every leaf of
  the magus command tree declares one.
