### Changed

- **An exited job no longer blocks another fork's write paths.** Its holder returned, so
  the shared-checkout refusal and MGS3032 leave its paths alone.
