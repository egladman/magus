### Fixed

- **A resumed worker is told to end its first row before taking the next.** A `job exec`
  of another job while the held row is in flight names `magus job exit <held> --stdin`
  instead of stopping the worker, and `spawn-warm-worker` asks the resume to say the same.
