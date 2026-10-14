### Changed

- **`magus\job.put` and `magus job apply` refuse an unscoped job, as `magus job fork`
  does.** A new job that names no write paths and is not read-only is refused at every
  door. A fork onto a job that ended is held to the rules and limits of a new fork, and a
  merge that moves a job's write paths records a fresh write proof.
