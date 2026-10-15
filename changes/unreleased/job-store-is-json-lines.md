### Changed

- **Breaking: the job store is a JSON Lines file, `jobs.jsonl`.** Each line is one version of one row, and a row's last line is the row, so `grep` and `tail` read a job's history without magus.
