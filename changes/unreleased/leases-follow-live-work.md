### Changed

- **A job that filed its result no longer holds a run's outputs.** Only a declared or
  running job owns its write paths, for a write and for a run that regenerates an output
  in them alike, so an exited job waiting on `magus job wait` blocks nobody's run.
