### Added

- **MGS4008: an unschedulable composed step is refused before it runs.** Two targets in one
  step, one writing what the other reads with no `ctx.needs` path between them, fail at
  derivation. `magus doctor` checks every composed target.
