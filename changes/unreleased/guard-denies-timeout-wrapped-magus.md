### Added

- **The agent guard refuses magus wrapped in `timeout` or `gtimeout`.** A `run` or
  `affected` is served the same command with `--timeout` and the wrapper's duration
  (`timeout 600` becomes `--timeout 10m`); any other verb is served bare. `magus buzz`
  is advised instead. A QUIT or ABRT signal and a verb that runs until interrupted pass.
