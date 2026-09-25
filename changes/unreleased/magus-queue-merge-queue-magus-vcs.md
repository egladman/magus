### Added

- **`magus queue` is a merge queue; `magus vcs queue` is gone.** Its `ls`, `plan`,
  `validate` and `apply` read JSON and report JSONL. Validation runs changes' code with
  read access only; apply rebuilds each candidate and merges it with the change's own
  method. The code is `internal/queue`, its contract and mocks in its `types` package;
  `--facts` serves other build tools.
