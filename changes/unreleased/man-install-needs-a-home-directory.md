### Changed

- **`magus man install` fails when no home directory resolves.** Without `--dir` it used to
  fall back to a relative `share/man/man1`; it now returns the error and says to pass
  `--dir`. `libs/xdg` gains `DataDir` for the default.
