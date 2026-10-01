### Added

- **`flags\parse` reads repeated flags and commands.** Flags named in `repeated` collect
  every value in order into `lists` (`--env A --env B`). With `command` set, the first
  bare word starts a command that keeps its own flags, the way sudo and Go's flag package
  read argv. `on-linux` and `on-actions` now parse with it.
