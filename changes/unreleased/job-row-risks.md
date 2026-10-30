### Fixed

- **MGS3045 keeps a root target that spells out a sub-project's path.** `lint .` reads
  `libs/**/*.go` and `test .` reads `docs/reference/codes/**/*.md`, so a write there gates
  the root target as well as the owner's. An owner whose target does not read the file
  leaves it to the root, and each gate names its own source.
- **MGS3045 says when it cannot place a gate.** A project binding spells it carries
  unresolved cannot say whether it runs a target, so the gate stays on the ancestor. The
  fork and apply now name that project among the checks they did not run.
