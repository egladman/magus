### Changed

- **Breaking: MGS3018: a job's write paths name files.** `magus job fork`, `magus_job`
  and `magus\job.put` refuse a write path that is an existing directory, unless it is a
  project root the job owns whole or does not exist yet. A glob ending in wildcards
  (`dir/**`) is judged as its directory.
