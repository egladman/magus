### Changed

- **Ending a job that already ended is no failure.** `magus job exit` prints
  `<id> already ended <state> <ago>` and exits 0; with `--stdin` it is still refused.
