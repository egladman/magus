### Changed

- **`WithFixedDelay` now applies to `Do`, and `os\retry` shares the stop and the backoff arithmetic.** A host error raised inside its callback keeps its type, and a value the script throws, a string included, is retried.
