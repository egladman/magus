### Fixed

- **A target no longer replays another target's declared output.** A magusfile target
  that inherits a project's outputs no longer records or restores a glob a sibling
  declares, unless it composes that sibling. A cached `lint` had been writing a stale
  `gen/**` back over a regenerated one, so `generate`'s drift check failed on every run.
