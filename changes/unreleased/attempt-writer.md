### Added

- **`job.Store.RecordAttempt` records an attempt a host ran itself.** A host that starts
  its own workers writes one `job.Attempt` per try. The store refuses an unknown job, an
  `N` that is not the next try, a start after the end and an end it does not record.
  `Store.AppendAttempts` stays unexported.
