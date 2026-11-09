### Changed

- **Breaking: `magus job apply` refuses a record that releases a write path and drops goals.**
  A record is the whole spec, so narrowing its paths cleared every goal it left out. It is now a
  `JobRecordInvalid` problem with one `add_goal` edit per dropped goal, and `--fix` restores them
  all. Dropping goals without releasing a path is unchanged.
