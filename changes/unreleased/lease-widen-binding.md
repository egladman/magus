### Fixed

- **A returned job's holder writes in the checkout it took the job in.** The guard
  placed a lease's paths in the row's checkout only while the row was declared or
  running, so a holder continued after `magus job exit` was refused. It now reads the
  checkout off the row in any state.
