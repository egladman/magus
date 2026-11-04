### Fixed

- **A background symbol refresh gives way to a run.** A `magus run` that met a detached
  `refs --refresh` holding its project lock was refused with exit 75 for up to three
  minutes. It now asks the refresh to stop, says which holder it waits on and for how long,
  and takes the lock.
