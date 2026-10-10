### Changed

- **Every stderr line but help is a log record.** `-q` and `-s` now hide notes and keep
  hints and next commands, `-o jsonl` carries them, and secrets are redacted in them.
  Default output keeps its words. The console line after a job verb moves to stderr.
  The `stderrprint` and errmsg `error-notice` rules keep it that way.
