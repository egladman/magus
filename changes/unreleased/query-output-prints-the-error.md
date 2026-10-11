### Added

- **`magus query output <ref>` prints a failed run's error.** The text form logs the
  recorded error after the log, so a check that fails with an empty log, such as a
  generate drift check, says why. Stdout is unchanged.
