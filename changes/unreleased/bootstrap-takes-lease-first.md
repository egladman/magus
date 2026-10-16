### Changed

- **The worktree bootstrap takes the job's lease before it builds the graph.** The order
  is place `./magus`, install harness skills, `job exec`, the fit check, then the graph
  build, so an edit lands after the base is recorded. A failed graph build names
  `./magus graph build` and does not fail the bootstrap. Its last line says edits may
  begin.
