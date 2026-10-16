### Changed

- **`magus job fork` refuses a row with no write paths unless it is `--read-only`.** Name
  the paths the holder writes, or mark a job that only reads. A read-only job passes only
  by a script check, or its holder ends it with `magus job exit`.
