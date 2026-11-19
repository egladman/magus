### Changed

- **The console returns errors as values.** Reads below the entry return `Result` instead of
  throwing or swallowing a failure, the test doubles reject rather than throw, and the
  console's `no-throw` baseline is gone.
