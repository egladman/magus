### Changed

- **Breaking: job rows are `schema_version` 11.** A `#` in a write path now claims a
  declaration, which an older magus would match against no file and stop grading, so it
  refuses these rows instead. Records sent to `magus job fork --stdin` and `magus_job`
  carry 11. Version 10 is skipped: a store row already carries it from an unmerged branch.
