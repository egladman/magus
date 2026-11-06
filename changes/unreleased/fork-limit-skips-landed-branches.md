### Fixed

- **The finished-branch fork limit skips a branch its base already holds.** A branch whose
  merge into `HEAD` or the VCS base leaves that tree unchanged, as a squash merge leaves
  it, stops counting, and the refusal says how many it skipped.
