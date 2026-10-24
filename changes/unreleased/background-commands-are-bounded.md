### Added

- **The guard names a background task that has gone silent.** A root session's task that
  wrote nothing for 30 minutes is named once with its `TaskStop` call and output file;
  past 60 minutes non-read calls are refused. `background-unbounded` offers
  `magus job exec --timeout` at `ceiling_minutes`, and `filter-without-input` catches
  interpreters waiting on stdin.
