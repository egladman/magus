### Fixed

- **`magus job wait` accepts a run recorded in the same second as the job was
  declared.** The declaration time is stored in whole seconds, and the comparison
  rounded it up to the end of its second, so a check run straight after `job fork` or
  `job apply` was rejected as older than the job.
