### Changed

- **A fork whose workspace rules do not load keeps its row.** `job fork --check` refused
  with MGS3039 when the checkpoint's magusfiles needed a newer binary. The fork now stores
  the row, says the check was not graded, and leaves the guard to judge it with the
  checkout's own binary.
