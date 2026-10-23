### Added

- **A worker widens its own job when magus proves the path belongs to it.** A
  `magus job apply` record carrying only `id` and `widen` adds each path with its proof:
  `refs:<symbol>`, `test:<file>`, `output:<project>:<target>` or `released`. The store
  re-proves it and records it on `widened`; the new `lease-widen` guard rule serves that
  apply line instead of sending the worker away.
