### Changed

- **Breaking for SDK callers: a job's check is opaque JSON.** `Job`, `Goal`,
  `Declaration`, `Put`, `Statement` and `Report` carry `Check` as `codec.RawMessage`;
  `LeaseCheck`, `ParseLeaseCheck`, `CheckError` and `SameLeaseCheck` are gone.
  `job.Open` takes `WithCheckSpec(validate, equal, render)`, and without it every row
  write and grading call refuses.
- **Breaking for SDK callers: `safetyscissors.New` requires `WithCheckSpec`.** It passes
  the spec to the job store and names the option when it is missing or holds a nil
  function.
- **Breaking for SDK callers: the store judges only a check's shape.** `Faults` keeps
  the structural faults alone, a check the caller's spec refuses fails the write with
  `*CheckRefusedError`, and `Goal.Subject()` is nil for a check goal.
- **Breaking for SDK callers: `ChangedFields`, `SpecChanges` and `AuthorizeRow` take a
  `CheckEqual`.** A goal's check is compared through it, so a bound worker that changes
  any part of a goal's check changes its goals.
- **Breaking: the job store migrates to snake_case.** Migration 10 respells every
  jobs.jsonl and attempts.jsonl line, header included: keys through an explicit table,
  CamelCase enum values back to their Go values, and `noDefaultCharms` inside each
  check. A value no enum lists is refused. An older magus refuses the migrated store as
  newer.
- **`types.LeaseCheck` and the `types` job views read the opaque check.** `types.Job`,
  `types.Goal`, `types.JobList` and `types.Declaration` decode it as a lease check, and
  `-o yaml` prints a row's check as an object.
- **`ValidateDeclaration` moves to `internal/job`.** It reports a check that does not
  read as a lease check; `types.DeclarationProblems` and `types.ValidateGoal` are gone.
- **A check over the workspace root renders as `magus run <target> --all`.** The
  validation line and the command the guard names both spell it so.
- **buzzgen and `jobschema` follow Go's field-visibility rule.** An embedded field
  hidden by a shallower field of the same name is not mirrored or published, and the
  shallower one takes its place. `buzzgen.MirroredFields` is exported.
