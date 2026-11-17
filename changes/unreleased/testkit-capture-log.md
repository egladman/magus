### Added

- **`testkit.CaptureLog` captures the default `slog` logger.** It returns the records a function logs at or above a level, and restores the logger and the `log` package afterwards.
