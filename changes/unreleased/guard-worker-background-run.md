### Added

- **The guard refuses a worker's backgrounded magus run.** A leased worker that runs its
  check in the background ends its turn and parks. The new `worker-background-run` rule
  denies a worker's `magus run` or `magus affected` when the wiring reports the host
  backgrounds it (`magus shell --call-background`); hook template version 23 forwards it.
