### Added

- **Every harness spell wires a rate-limited disk hook that reclaims space in small chunks.**
  Once a turn it costs one stat; past `disk.sample_every` (default 10m) it samples the free
  space of the workspace's filesystem, and below `disk.min_free_mb` (default 51200) or
  losing space faster than `disk.growth_mb_per_hour` (default 20480) it starts one reclaim
  chunk in the background: the two oldest removable worktrees and the agent branches nothing
  needs, all within five minutes. It trims no toolchain's cache. It says one line
  only when it started a chunk or free space is below `disk.critical_free_mb` (default
  10240). It needs no daemon, and `magus doctor` shows the trend.
