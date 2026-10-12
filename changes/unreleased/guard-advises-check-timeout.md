### Added

- **The guard names a timeout that covers a worker's check.** The `check-timeout` advisory
  reads the target's last real run and advises a timeout of half again that, capped at the
  host's foreground maximum. The Claude Code harness declares its limits, and the glue
  passes them as `--foreground-default-ms` and `--foreground-max-ms`.
