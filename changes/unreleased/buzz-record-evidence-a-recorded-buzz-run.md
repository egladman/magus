### Fixed

- **A recorded Buzz run is evidence for one job and one argument list.** The record holds
  the script's arguments and the lease it ran under. A script check rejects a run with
  other arguments, under another job's lease, or under none, and the violation says which.
