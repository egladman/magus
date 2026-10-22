### Fixed

- **A returned job's holder stays held to its write paths.** The guard read an exited
  job as unbound, so its holder could write outside its paths unchecked. Every guard
  rule now finds the acting job's row one way, and `magus job exec` in the job's own
  checkout sets an exited job running again.
