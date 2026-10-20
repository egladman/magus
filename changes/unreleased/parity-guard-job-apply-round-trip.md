### Fixed

- **`magus job apply -f` accepts what `magus describe job -o json` prints.** The members
  the job store adds, such as `write_proof`, are dropped on read instead of refusing
  the record.
