### Added

- **Every harness spell wires a rate-limited disk hook.** Past `disk.sample_every` it
  samples free space, and below `disk.min_free_mb` or past `disk.growth_mb_per_hour` it
  reclaims the two oldest removable worktrees and unneeded agent branches in the
  background. It needs no daemon, and `magus doctor` shows the trend.
