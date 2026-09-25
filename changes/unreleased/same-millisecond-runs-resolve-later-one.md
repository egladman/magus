### Fixed

- **Two runs of one step recorded in the same millisecond resolve to the later one.**
  A bare output ref and `--attempts` broke that tie by a random attempt hash, so they
  could answer with the older run. Each record now stores when it was persisted, and
  that decides the tie.
