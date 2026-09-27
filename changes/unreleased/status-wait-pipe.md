### Added

- **`magus status --wait | magus run <target>` waits for the build budget.** A run
  refused with MGS3009 still exits at once and names this pipe. `status --wait` sizes the
  run's claim from the pipe and exits once the budget can seat it; on its own it waits
  for a free slot.
