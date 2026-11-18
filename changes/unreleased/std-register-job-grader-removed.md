### Removed

- **Breaking for SDK callers: `std.RegisterJobGrader` is gone.** So is the process-global
  `internal/job.SetCheckGrader` it wrapped. A store grades a declared check with
  `job.Location.Grader`; `magus\workspace().putJob` passes one through
  `magus.Magus.CheckGrader`. With none, the fork reports one MGS3039 problem and a gate
  postcondition is offered ungraded.
