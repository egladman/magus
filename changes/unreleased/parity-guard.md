### Fixed

- **CI failures show actionable diagnostics with fewer repeated notices.** Read
  failed targets through the artifact reader, prioritize findings, and report each
  stripped environment set once per target while preserving metrics. Fix guard
  integration, lint, attribution assertions, and the Linux process-group probe.
  Shorten ranking filenames to `results.go` and keep evaluation helpers in tests.
