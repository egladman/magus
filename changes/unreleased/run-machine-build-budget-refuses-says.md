### Fixed

- **A run the machine's build budget refuses says so.** It exited 75 with nothing after
  the header; it now prints `[fail] <project> <target> (not started)` with the MGS3009
  cause naming the holder, and `-o jsonl` emits the `run.target.result` and
  `run.diagnostic` records.
