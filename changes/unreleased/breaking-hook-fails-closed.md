### Changed

- **Breaking: a guard hook that cannot judge refuses its call.** An unreadable magus.yaml,
  a bad `MAGUS_*` value, a crash, a timeout or a missing binary now blocks, on every host.
  Only the workspace's repair passes: the bootstrap in a checkout of magus, `magus self
  update` elsewhere. A stop ends unjudged. Run `magus agent harness install`.
