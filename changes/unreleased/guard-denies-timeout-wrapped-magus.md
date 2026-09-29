### Added

- **The agent guard refuses magus wrapped in `timeout` or `gtimeout`.** The wrapper
  kills magus from outside, so the run log records no cause and a target's tools can
  outlive it. A `run` or `affected` is served the same command with `--timeout` and the
  wrapper's duration (`timeout 600` becomes `--timeout 10m`); any other verb is served
  bare. `magus buzz`, which has no timeout of its own, is advised instead. A QUIT or ABRT
  signal and a verb that runs until interrupted pass.
