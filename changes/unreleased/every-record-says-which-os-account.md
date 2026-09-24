### Added

- **Every record says which OS account wrote it, and through which entry point.** Trail
  events, session records and job rows carry `user`, `uid` and `entry_point` (`cli`,
  `hook`, `mcp`, `rpc`, `daemon`), read by magus itself. `magus session` gains a USER
  column. A job row's `registered_by` is an `Origin`.
