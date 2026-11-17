### Changed

- **Breaking: `magus job apply` refuses a record that releases a write path and drops goals.**
  A record is the whole spec, so narrowing its paths cleared every goal it left out. It is now a
  `JobRecordInvalid` problem per dropped goal; keep the goal or release the path alone. Dropping goals without releasing a path is unchanged.
