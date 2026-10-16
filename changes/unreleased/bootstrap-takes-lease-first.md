### Changed

- **The worktree bootstrap takes the job's lease before it builds the graph.** The order
  is place `./magus`, install harness skills, `job exec`, the fit check, then the graph
  build, so an early edit lands after the base is recorded. A failed graph build prints a
  line naming `./magus graph build` and does not fail the bootstrap.
