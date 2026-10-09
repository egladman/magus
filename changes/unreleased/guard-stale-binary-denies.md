### Changed

- **The guard denies every write while its magus cannot load the workspace.** A binary
  older than the tree, or a checkout with no `./magus`, has edits, spawns, pushes and
  state-changing commands refused until a loadable binary exists; reads and the fix
  still run. Hooks find the binary by the nearest `magus.yaml`, so a call from
  `console/` uses the root's build, in the OpenCode plugin as in the Buzz glue. The
  magus MCP tools that write (`client`, `diff` with an `op` other than `state`) are
  denied the same way, where `config`, `console` and `buzz` no longer are. The hook
  templates are now version 21, so `magus doctor` flags a copy still walking to the
  nearest `magusfile.buzz`.
