### Changed

- **Breaking: `magus job apply` refuses a record that releases a write path and drops postconditions.**
  A record is the whole spec, so narrowing its paths cleared every postcondition it left out. It is now a
  `JobRecordInvalid` problem per dropped postcondition; keep the postcondition or release the path alone. Dropping postconditions without releasing a path is unchanged.
