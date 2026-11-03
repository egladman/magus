### Added

- **`max_attempts` on `magus\workspace().putJob` and a fork record** caps how many times a job may be forked. It is checked only when an ended job is forked again; magus never retries a job on its own.
