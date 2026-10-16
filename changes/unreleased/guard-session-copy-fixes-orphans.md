### Fixed

- **No guard dry run's session copy outlives its process.** Each copy names the process
  that made it, and the next dry run removes the copy of a process that has exited.
