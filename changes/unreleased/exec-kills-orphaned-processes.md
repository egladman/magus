### Fixed

- **A command magus runs leaves no process behind.** On Linux, macOS and the BSDs,
  whatever of a target's process group outlives its command is killed when the command
  exits, before it is reaped, so a background process it started stops there and no
  longer holds the run open for five seconds on its output.
