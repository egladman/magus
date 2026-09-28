### Changed

- **The guard refuses magus runs chained with `&&` or `;`.** It serves the pipe of the
  same stages, which runs overlapping projects in order and exits with the first failure.
  A stage on disjoint projects still runs after an upstream fails. `||`, a later
  `affected` stage and a stage after relinking `./magus` stay advisories.
