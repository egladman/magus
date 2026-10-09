### Changed

- **The guard denies every write while its magus cannot load the workspace.** A binary
  older than the tree, or a checkout with no `./magus`, has edits, spawns, pushes and
  state-changing commands refused until a loadable binary exists; reads and the fix
  still run. Hooks find the binary by the nearest `magus.yaml`, so a call from
  `console/` uses the root's build.
