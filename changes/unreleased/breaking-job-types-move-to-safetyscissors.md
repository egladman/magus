### Changed

- **Breaking for SDK callers: the job record types live in `libs/safetyscissors/job`.**
  `types.Job`, `Declaration`, `Goal`, `LeaseCheck`, `Deviation`, `Attempt`, `Origin`,
  `Credential`, the claim grammar (`SplitClaim`, `Location`, `FileChange`, `RegionChange`)
  and the rest of `types/job.go`, `job_grant.go`, `job_put.go`, `job_reader.go`,
  `lease_context.go`, `change.go`, `attempt.go`, `origin.go` and `grant.go` move there.
  Each name loses its `Job` prefix: `JobState`, `JobPaths`, `JobResult`, `JobStatus`,
  `JobGrant`, `JobPut`, `JobReader`, `JobList`, `JobAttempts`, `WithJobLease`,
  `JobLeaseFromContext`, `ValidJobID` and their kin are `job.State`, `Paths`, `Result`,
  `Status`, `Grant`, `Put`, `Reader`, `List`, `Attempts`, `WithLease`, `LeaseFromContext`,
  `ValidID`. `JobAttempt` is `job.CapturedRun`, beside `job.Attempt`. A credential's
  `types.Grant` is `job.Access` (`GrantOperator` is `AccessOperator`), since `job.Grant`
  is a job's rule grant. `agent.Spec` and its `Model` and `Worker` are `job.Agent`,
  `Model` and `Worker`, and `types/agent` is gone. `BriefFormat`, `RetryVerdict`,
  `RetryChange` and `ChangeStatus` move with the records that carry them.
  `job.APIVersion` and `ResultAPIVersion` are untyped, and the record's api_version stays
  `magus.job/v1alpha1`.
- A record's own faults are `job.Fault` values that name no code. magus reports them as
  MGS3037 and MGS3038 through `types.DeclarationProblems`, `ValidateDeclaration`,
  `ValidateGoal` and `FoldLegacyNames`, which replace the `Declaration` and `Goal` methods.
  `JobProblem`, `JobValidation`, `Permissible` and `ParseJobTimeout` stay in `types`.
  No alias remains; change the import and the name. Buzz names and the JSON records are
  unchanged.
