### Added

- **`magus job apply <id> --add-write-path <path>` widens one job without a record.** It
  applies the job's own spec plus the paths, so every refusal of a whole record holds. The
  flag repeats or takes a comma list, and cannot be combined with `-f`.
