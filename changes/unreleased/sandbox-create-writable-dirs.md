### Fixed

- **A sandboxed tool can create the cache directory it was granted.** The kernel sandbox
  skipped a granted path that did not exist yet, so a tool whose cache directory was
  missing (buf on a fresh machine) was denied creating it; a missing writable path is now
  created before it is granted.
