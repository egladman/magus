### Fixed

- **A returned job's holder writes in the checkout it took the job in.** The guard placed
  a lease's paths in the checkout its row records only while the row was declared or
  running, so a holder continued after `magus job exit`, often to write the paths a
  `magus job apply` had just added, was refused "in another checkout than lease X's" and
  sent to the session's checkout. It now reads the checkout off the row whatever its state.
