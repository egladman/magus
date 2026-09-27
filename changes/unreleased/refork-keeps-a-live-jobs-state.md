### Changed

- **Re-forking a live job to move its paths keeps its state.** A fork that only changes
  write, read or deny paths no longer resets the job to `declared`.
