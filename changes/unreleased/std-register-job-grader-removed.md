### Removed

- **Breaking for SDK callers: `std.RegisterJobGrader` is gone.** A job store grades a declared check with the grader its `job.Location.Grader` names; cmd/magus, the server and `magus\workspace().putJob` pass one, and a store opened with none grades nothing.
