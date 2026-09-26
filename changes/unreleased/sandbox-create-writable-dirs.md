### Fixed

- **A sandboxed tool can create the cache directory it was granted.** The kernel sandbox
  skipped a granted path that did not exist yet, so a tool whose declared cache directory
  was missing (buf on a fresh machine) was denied creating it. A missing path a spell or
  `sandbox.allow` declares writable is now created before it is granted; a missing
  workspace or git directory is still reported, never made.
