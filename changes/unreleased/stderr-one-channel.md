### Changed

- **Every stderr line but help is a log record.** Warnings, hints, `next:` commands,
  console links and progress notes now pass through the display, so `-q` and `-s` hide
  the ones that inform and keep hints and next commands, `-o jsonl` carries them as
  notices, and secrets are redacted in them. Default output keeps its words; a notice may
  gain its component name or color. The console line after a job verb moves from stdout
  to stderr. The `stderrprint` linter reports a new direct write, and errmsg's
  `error-notice` reports an error built into a notice's message.
