### Fixed

- **A magus stage slow to start no longer loses the stage before it in a pipe.** A
  writer now waits for its reader to exec and to prove it, rather than 250ms, so a
  loaded machine no longer turns records into prose, drops a run's scope, or lets
  `magus affected` miss a failed upstream (MGS3030).
