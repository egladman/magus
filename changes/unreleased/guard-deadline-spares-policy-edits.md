### Fixed

- **A slow guard rule can be fixed.** When the hook deadline runs out while judging a write
  to a file the guard loads itself, the root magusfile or a source it imports, the write
  is advised instead of denied. Every other write that runs out of time is still denied.
