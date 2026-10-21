### Changed

- **A run that uses more memory than its curve predicted is charged its full peak.**
  The broker charges that run its full peak from the moment its own process tree goes
  over the predicted value, until the claim is released. Nothing is paused or killed,
  and host free memory is never read. A broker older than the client ignores the report
  and the client says so once.
