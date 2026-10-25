### Changed

- **The session heartbeat store stays bounded.** Past 1024 sessions and leases it is
  rewritten to the newest, dropping stamps older than a week.
