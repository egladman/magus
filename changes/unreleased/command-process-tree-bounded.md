### Fixed

- **A command's process tree is capped and reaped, setsid children included.** On linux
  and darwin magus follows each process a command starts by pid and start time. Past 1,024
  live processes it kills the tree and fails, naming the binary most of them run. When the
  command ends it kills every member still alive.
