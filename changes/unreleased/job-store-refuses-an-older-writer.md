### Added

- **An older magus no longer writes a job store a newer one wrote.** Each write stamps its
  build's commit and commit time on the store's header. A build from an older commit, or
  an unstamped one after a stamped write, is refused with MGS3051 naming both builds.
  Reading works with any build.
