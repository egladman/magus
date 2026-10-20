### Changed

- **Breaking: a wrongly typed or negative `os\retry` backoff option is refused.**
  `backoff_ms` and `max_backoff_ms` fell back to their defaults when given a string or
  null; they now raise an error naming the key, before the callback runs.
