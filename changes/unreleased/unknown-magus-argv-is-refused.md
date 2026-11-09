### Added

- **The `unknown-magus-argv` guard rule refuses a magus call the binary would
  reject.** A command or flag absent from the judging binary's command tree is
  denied, naming the nearest command and the build that judged.
