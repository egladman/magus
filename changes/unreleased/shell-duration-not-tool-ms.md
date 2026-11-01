### Changed

- **Breaking: `magus shell --tool-ms N` is `--duration <dur>`, and `--call-timeout-ms N` is
  `--call-timeout <dur>`.** Flag names no longer encode a unit. A Duration flag accepts a Go
  duration (`420ms`, `10m`) or a bare number counted in its declared `Unit`; help names that
  unit ("Bare numbers are milliseconds"). The PostToolUse recorder is `magus-duration.buzz`.
  A vocabulary test refuses new `*-ms` flag names. See
  [Postel's law](../../docs/design/principles/postels-law.md).
