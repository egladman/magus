### Added

- **The job API serves a run's attempt id, duration, revision and dirty flag.** The
  daemon's `JobAttempt` message now carries the four fields the job store records, so
  the console and the Claude Code mod no longer lose them. The job row also serves
  `waiter_evidence`, and a release says whether it was revoked.
