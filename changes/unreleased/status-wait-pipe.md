### Added

- **`magus status --wait | magus run <target>` waits for the build budget.** A run
  refused with MGS3009 still exits 75 at once, and its message now names this pipe.
  `status --wait` reads the run's command from the pipe, sizes its claims the way the run
  does, and exits 0 once the budget can seat them; the run starts only after it exits,
  and starts nothing (MGS3030) if the wait fails. On its own, `status --wait` waits for
  a free slot.
