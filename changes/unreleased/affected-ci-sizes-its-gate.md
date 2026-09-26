### Added

- **`magus affected ci` sizes its gate to the change.** Every changed file gets a tier
  (trivial, mechanical, scoped, full) with its evidence. Below full the gate runs only
  the drift check and lint, the targets that declare a changed doc, or `test` with
  `go-test` narrowed through `go list`. A trivial change exits 0.
  `--no-redundancy-check` runs the full gate.
