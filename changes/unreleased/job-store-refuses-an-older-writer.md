### Added

- **An older magus no longer writes a job store a newer one wrote.** Every write stamps
  its build's commit and commit time on the store's header line. A build from an older
  commit, or one carrying no commit once a stamped build has written, is refused with
  MGS3051, which names both builds: run the command with the newer `./magus`, or rebuild.
  Reading the store still works with any build.
