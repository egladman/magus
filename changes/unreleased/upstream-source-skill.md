### Added

- **The `magus-upstream-source` skill reads magus's own code at the binary's commit.**
  It is a last resort for a crash or for behavior the docs cannot explain: it fetches
  the exact commit outside the workspace and refuses a dirty or unpushed build.
