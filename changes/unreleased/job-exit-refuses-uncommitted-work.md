### Fixed

- **`magus job exit` refuses a result while the job's work is uncommitted.** It names the
  paths, and the row keeps its state so its holder commits and files again.
