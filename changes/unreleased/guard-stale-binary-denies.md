### Changed

- **The guard denies every write while its magus cannot load the workspace.** Reads
  and the fix still run. Hooks find the binary by the nearest `magus.yaml`, so
  `console/` uses the root's build. The writing MCP tools are denied the same way.
  Hook templates are now version 21; `magus doctor` flags an older copy.
