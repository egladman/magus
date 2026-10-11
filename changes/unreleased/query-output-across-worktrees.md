### Added

- **`magus query output <ref>` reads a ref another worktree ran.** On a miss it finds the
  checkout through the repository's session store, reads that checkout's output store
  without writing to it, and says on stderr which checkout it read. `--attempts`,
  `--identity`, `-o json` and `describe target --cache --against` resolve the same way.
- **`magus query output <ref>` prints a failed run's error.** The text form writes the
  recorded error to stderr after the log, so a check that fails with an empty log, such as
  a generate drift check, says why. Stdout is unchanged.

### Changed

- **A ref that no store holds names the checkout that ran it.** The MGS8001 message gives
  that checkout and `cd <checkout> && magus query output <ref>`, also when the checkout is
  gone, and gives the command alone when no checkout recorded the ref.
- **`magus query invocation <id>` shows the project, target and output ref on each result
  line.**
