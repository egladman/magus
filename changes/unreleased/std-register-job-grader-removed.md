### Removed

- **Breaking for SDK callers: `std.RegisterJobGrader` is gone.** The composition root installs the check grader with `job.SetCheckGrader`, and a job store opened without a grader of its own grades with it.
