### Fixed

- **A squash of a binary's own branch no longer reads as a newer load contract.** The
  skew check counts only paths whose content differs between the build and HEAD.
