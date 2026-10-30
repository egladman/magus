### Fixed

- **A recorded Buzz run always prints its ref, or fails saying why.** `magus buzz --record`
  and `-t` wrote the ref through the log, which `MAGUS_LOG_SILENT` drops, so the ref
  vanished. It now goes to stderr directly, and a run that cannot be stored exits non-zero.
- **A recorded Buzz run is evidence for one job and one argument list.** The record holds
  the script's arguments and the lease it ran under. A script check rejects a run with
  other arguments, under another job's lease, or under none, and the violation says which.
