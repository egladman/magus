### Changed

- **`magus\job.put` and `magus job apply` refuse an unscoped job, as `magus job fork` does.** A new job that names no write paths and is not read-only is refused at every door.
