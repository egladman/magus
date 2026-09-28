### Changed

- **Breaking: MGS3018: a job's write paths name files.** `magus job fork`, `magus_job`
  and `magus\job.put` refuse every existing directory, `.` and a sub-project root such
  as `console` included, since a root overlaps every other job. A path the job creates
  passes. A glob ending in wildcards (`dir/**`) is judged as its directory.
