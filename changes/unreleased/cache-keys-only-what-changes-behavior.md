### Fixed

- **A magus rebuild, the `rw` charm and a linter upgrade no longer invalidate unrelated
  entries.** A target that asks the binary for its answer keys on the binary's catalogs,
  not its build. A write grant keys only a step that reads it, so a local `rw` run and
  the `ci` gate share entries. golangci-lint's version keys only the targets that run it.
