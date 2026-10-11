### Fixed

- **A slow guard rule can be fixed.** When the working tree's rule runs out of the hook
  deadline on a write to the root magusfile or a source it imports, the write is advised
  instead of denied, once the committed rule has answered. Every other write that runs
  out of time is still denied.
