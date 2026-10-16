### Changed

- **A job record reports every problem at once, each with a code.** `magus job fork`
  and `magus job apply` used to stop at the first bad field of a record. They now list
  every one: a field that fails its shape is MGS3037, a goal no evidence could grade is
  MGS3038, and each problem says how to fix it.
