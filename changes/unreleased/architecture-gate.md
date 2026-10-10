### Added

- **An agent asked a structure question loads the architecture skill first.** The
  `architecture-unbriefed` guard rule holds the next call until magus-architecture-review
  loads, after a message about imports, boundaries or layering, or a write that creates a
  directory. `magus job fork` names the generated outputs a job's write paths rebuild
  outside them.
