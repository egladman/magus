### Fixed

- **Ending a job unbinds the session that held it.** A client script's
  `magus\job.exit` now releases the binding as `magus job exit` does, and a holder
  whose job a wait passed or failed is released on its next call. A rejected wait
  leaves the row exited, so its holder stays bound.
