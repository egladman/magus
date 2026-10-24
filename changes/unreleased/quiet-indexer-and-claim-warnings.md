### Changed

- **Symbol indexers print nothing on success, and progress redraws collapse in logs.**
  Every spell's indexer runs quietly and still prints stderr on failure. A command's
  carriage-return redraws reach a non-terminal as their last frame only, and
  `--log-level=debug` keeps them all.
