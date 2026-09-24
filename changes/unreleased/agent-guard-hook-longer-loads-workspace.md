### Changed

- **The agent guard hook no longer loads the workspace.** `magus shell` reads its rules
  from the root magusfile alone, and loads the committed copy only while a file that
  load read is uncommitted. Measured here: about 120ms per call, down from about 1.6s.
