### Added

- **`magus session -o json` records what each target claimed and cost.** A target
  result carries `started_ms`, the `slots` and `memory_mb` it was granted, their
  `sizing` (`declared`, `measured` or `none`), `waited_ms` for its seat, and its
  `max_rss_bytes` and `cpu_ms`. A replay claims nothing and says `none`.
