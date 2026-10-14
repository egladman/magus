### Added

- **Job rows carry generations and attempts (schema 13).** `generation` counts changes to
  a job's declared spec, as a Kubernetes object's does, and each result, run record and
  grade records the `observed_generation` it was taken against, so evidence from an
  earlier spec reads as stale. `attempts`, `prior` (the last three ended attempts as
  unmet goals, violations, risks, denials and failing refs, never a transcript) and
  `rejection` record what a re-forked job inherits. A declaration may set
  `max_attempts`, checked only when the job is forked again: magus never retries a job
  on its own. A row needing these is read-only to an older magus.
- **One retry classification.** An error is retryable unless typed otherwise, and a
  diagnostic code declares itself not retryable once, naming what must change first: a
  config edit, a widening, a person or an upgrade.
