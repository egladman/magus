### Changed

- **A leased worker may run magus in a scratch workspace of its own.** A workspace
  under the magus tmp dir that is no checkout of the job's repository is neither a
  foreign checkout nor a lease violation. Another checkout of the same repository
  stays refused, there as anywhere.
