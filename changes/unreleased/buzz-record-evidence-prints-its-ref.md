### Fixed

- **A recorded Buzz run always prints its ref, or fails saying why.** `magus buzz --record`
  and `-t` wrote the ref through the log, which `MAGUS_LOG_SILENT` drops, so the ref
  vanished. It now goes to stderr directly, and a run that cannot be stored exits non-zero.
