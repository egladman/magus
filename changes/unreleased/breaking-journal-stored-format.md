### Changed

- **Breaking: `dur_ms` is no longer read.** A journal or activity log line that spells
  its duration `dur_ms` reads with a zero duration; `duration_ms` reads as before. None
  of the stores measured carried one.
- **Breaking: a `warn` journal line reads as a kind this build does not know.** Nothing
  writes one, and every accessor of such a record reports false.
- **Breaking: a `scope` journal line's `project` and `target` keys are ignored.** No
  producer sets them, and the console no longer queues a target from one.
