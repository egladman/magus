### Changed

- **Breaking: job goals live in the record's `goals`; the `--gate-*` flags are gone.**
  `completion_gates` is renamed, and `job fork` refuses a writing job with neither
  a check nor a goal. `job wait` grades the diff in the job's own checkout, and
  fails a symbol goal it cannot verify. A record naming no state is stored declared.
