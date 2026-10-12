### Fixed

- **A target that asks the magus binary for its answer keys on that binary.** A body that
  calls `magus\describe` or forks a nested magus, directly or through a helper or an
  imported module, gets a `binary:` key line naming the running build. A rules page
  rendered from `magus\describe.rule()` no longer replays the old catalog after a rebuild.
