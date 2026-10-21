### Changed

- **A run that uses more memory than its curve predicted is charged its full peak.**
  The charge starts when its process tree goes over the prediction, lasts until the
  claim is released, and carries over to a restarted broker. Nothing is paused or
  killed. A broker older than the client ignores the report, and the client says so
  once.
