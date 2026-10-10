### Changed

- **`ListJobs` serves every field `magus ls jobs` prints.** The job service now carries
  each row's end reason, write proof, base verdict, checkout, registration, attempts,
  entries and integration, and the overdue, orphaned, stale, blocked and read-only
  flags. All three readers build one report, so `magus\job.list` flags stale and
  overdue rows too.
