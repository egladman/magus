### Changed

- **Breaking: job postconditions live in the record's `postconditions`; the `--gate-*` flags are gone.**
  `completion_gates` is renamed, and `job fork` refuses a writing job with neither
  a check nor a postcondition. `job wait` grades the diff in the job's own checkout, and
  fails a symbol postcondition it cannot verify. A record naming no state is stored declared.
