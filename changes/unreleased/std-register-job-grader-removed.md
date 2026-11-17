### Removed

- **Breaking for SDK callers: `std.RegisterJobGrader` is gone**, with the process-global
  `internal/job.SetCheckGrader` it wrapped. A job store grades a declared check with the
  grader its `job.Location.Grader` names; cmd/magus, the server and
  `magus\workspace().putJob` pass one, the last through `magus.Magus.CheckGrader`. A store
  whose Location names no grader grades no declared check.
