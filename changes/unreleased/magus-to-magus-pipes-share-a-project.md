### Fixed

- **One magus can pipe into another that needs the same project.** The reader, proven
  from the kernel on linux and macOS, takes no lock until the upstream is done with the
  projects it needs, draining the pipe; before, whichever stage lost the race exited 75. Stages on different projects still stream. A looping pipe is MGS3023.
