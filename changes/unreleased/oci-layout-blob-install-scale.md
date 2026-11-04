### Changed

- **Image layouts hash each blob once.** Pull fetches straight into the new layout and
  verifies while it streams; push asks the registry before reading a blob and hashes
  only what it uploads, eight transfers at a time; assemble links a base's layers
  instead of copying them, and never changes the mode of a blob another layout owns.
