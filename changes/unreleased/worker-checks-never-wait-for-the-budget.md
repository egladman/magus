### Changed

- **A leased worker's check runs with `--budget-wait 0`, and the guard refuses a
  worker's run without it.** Runs now wait 30m for the build budget by default,
  and a worker must never wait. The check a brief or a denial prints carries the
  flag, and a worker's `magus run` that omits it is denied with the check named.
