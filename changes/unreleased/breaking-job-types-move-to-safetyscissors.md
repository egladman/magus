### Changed

- **Breaking for SDK callers: the job record types live in `libs/safetyscissors/job`.**
  `types.Job`, `Declaration`, `Postcondition`, `LeaseCheck`, `Deviation`, `Attempt`, `Origin`,
  `Credential` and the claim grammar move there. No alias remains; Buzz names and the
  JSON records are unchanged.
- **Breaking for SDK callers: each moved name drops its `Job` prefix.** `JobState`,
  `JobPaths`, `JobResult`, `JobGrant`, `JobList`, `WithJobLease` and `ValidJobID` are
  `job.State`, `Paths`, `Result`, `Grant`, `List`, `WithLease` and `ValidID`. `JobAttempt`
  is `job.CapturedRun`.
- **Breaking for SDK callers: `agent.Spec` is `job.Agent`, and `types/agent` is gone.**
  Its `Model` and `Worker` move with it. A credential's `types.Grant` is
  `internal/auth`'s `Access`, since `job.Grant` is a job's rule grant.
- **Breaking for SDK callers: a record's own faults are `job.Fault` values with no
  code.** magus reports them as MGS3037 and MGS3038 through `types.DeclarationProblems`,
  `ValidateDeclaration` and `ValidatePostcondition`, which replace the `Declaration` and `Postcondition`
  methods.
