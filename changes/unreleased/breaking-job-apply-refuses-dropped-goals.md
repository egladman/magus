### Changed

- **BREAKING: `magus job apply` refuses a record that releases a write path and drops goals.**
  A record is the whole spec, so a record written only to narrow the paths cleared every goal
  it left out. It is now a `JobRecordInvalid` problem with one `add_goal` edit per dropped
  goal, and `--fix` restores them all. Dropping goals without releasing a path is unchanged.

### Added

- **`magus job apply <id> --release-write-path <path>` drops write paths and keeps the rest of
  the spec.** It repeats or takes a comma list, and a value holding `#` is one declaration
  claim, so `--add-write-path` no longer splits `a.go#Run,Stop` either.
