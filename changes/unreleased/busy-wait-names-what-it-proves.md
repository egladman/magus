### Changed

- **busy-wait says what it proves about a foreign process.** A sleep loop probing
  `kill -0`, the process table, `magus status` or a job row is told it holds a tool slot
  for its whole wait, not that the process will announce its end.
