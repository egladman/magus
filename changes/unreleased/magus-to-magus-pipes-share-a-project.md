### Fixed

- **One magus can pipe into another that needs the same project, even through `jq` or
  `tee`.** The reader, proven from the kernel on linux and macOS, waits until the
  upstream is done with its projects, draining the pipe; before, the stage that lost
  the race exited 75. Different projects still stream. A looping pipe is MGS3023.
