### Changed

- **Breaking: MGS3018 refuses a project root as a write path.** `magus job fork`,
  `magus_job` and `magus\job.put` refuse every existing directory, `.` and a
  sub-project root such as `console` included: a root claims every file under it
  and overlaps every other job. List the files the job will edit.
