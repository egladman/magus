### Fixed

- **`log.level` is honored.** It was overwritten at startup by the level `-v` and `-q`
  imply, so `log.level: debug` in `magus.yaml`, `MAGUS_LOG_LEVEL` and `--log-level` left the
  process at `info`. A verbosity flag still wins when given.
