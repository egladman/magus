### Fixed

- **A lint goal grades what the job changed.** A lint run, or a run of any check whose args
  are all files, counts when it read every file the job changed, so a widening keeps it. A
  fork's lint goal leaves out tests it added itself, and `magus job exit` refuses an owed
  reason other than `no seat`.
