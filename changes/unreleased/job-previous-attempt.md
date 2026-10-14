### Added

- **`magus describe job` shows the previous attempt.** Forking a job that ended starts its
  next attempt and folds the one that ended into the row: its unmet goals, violations, the
  guard's denials of its holder, unresolved risks and failing run refs, and whether repeating
  it can help or what must change first. The next holder reads it there, never in a
  transcript. A job's `generation` counts changes to its spec, and `magus job wait` never
  passes evidence filed against an earlier one.
- **`max_attempts` on `magus\job.put` and a fork record** caps how many times a job may be
  forked. It is checked only when an ended job is forked again; magus never retries a job on
  its own.
- **The model-tier lens reports the first-attempt pass rate.** `first_attempts`,
  `first_pass` and `first_pass_lower` sit beside `pass_lower`, with `not_retryable` counting
  the endings that needed a change before another attempt could help.
