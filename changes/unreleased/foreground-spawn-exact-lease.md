### Fixed

- **A foreground worker is bound to the spawn for the job it declares.** Parallel foreground
  spawns no longer hand a worker the oldest waiting spawn's job. The worker's first call joins
  the one spawn whose title names its `BAGGAGE` lease, and is refused with the reason when it
  declares none, a job no spawn names, or a job two spawns name.
