### Changed

- **`magus job exit` ends several jobs in one call.** Without `--stdin` it takes any
  number of job ids and abandons each, trying every one and failing at the end if any
  could not be ended. A filed result still names exactly one job.
