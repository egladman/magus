### Changed

- **Breaking: `magus shell --tool-ms N` and `--call-timeout-ms N` become `--duration <dur>` and `--call-timeout <dur>`.**
  Duration flags accept Go durations (`420ms`, `10m`) or bare numbers in the declared unit;
  help names the unit ("Bare numbers are milliseconds"). The PostToolUse recorder is
  `magus-duration.buzz`. A vocabulary test blocks new `*-ms` flag names. See
  [Postel's law](docs/design/principles/postels-law.md).
