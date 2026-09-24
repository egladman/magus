### Added

- **A first SIGTERM drains the broker, bounded by `shutdown_grace`.** It turns new claims
  and services away, naming itself, and exits once its holders finish or the grace
  (default 5m, `MAGUS_SHUTDOWN_GRACE`) passes; `broker status` shows it draining. The
  server cancels its runs and waits the same grace. A second SIGTERM, or SIGINT to the
  broker, exits now.
