### Fixed

- **A sandboxed tool can create its declared cache directory.** The kernel sandbox skipped
  a granted path that did not exist yet, so buf on a fresh machine was denied its cache. A
  missing path a spell or `sandbox.allow` declares writable is now created; a missing
  workspace is still reported.
