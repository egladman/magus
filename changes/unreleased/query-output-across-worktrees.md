### Added

- **`magus query output <ref>` reads a ref another worktree ran.** On a miss it finds the
  checkout through the repository's session store, reads that checkout's output store
  without writing to it, and says on stderr which checkout it read. `--attempts`,
  `--identity`, `-o json` and `describe target --cache --against` resolve the same way.
