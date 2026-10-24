### Added

- **The guard claims a file one edge from a job's change instead of refusing the write.** A file referencing a declaration the diff changed, or testing a file the job changed, is claimed as `magus job apply` would. A generated output, magusfile, hub (25 or more users), another job's path or a job at `max_claims` is still refused, naming which.
