### Fixed

- **The holder of a `job fork --checkout` row gates from any directory.** The guard judges
  the directory each gate call runs in, following a leading `cd` and `-C` or `--root`,
  instead of the directory the host started in. `cd <checkout> && magus affected ci` passes
  for the holder from anywhere, and a gate aimed at another checkout is refused.
