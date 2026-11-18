### Changed

- **Breaking: the job row store lives in `libs/safetyscissors/job`.** `job.Open(dir,
  opts...)` opens jobs.jsonl, attempts.jsonl, grants.jsonl and the lock in the directory
  the caller names; magus passes the one it used, so rows stay put.
- **Breaking: the store's write pipeline and authorization moved with it.** `Update`,
  `Mutate`, `Rewrite`, `List`, `Attempts`, `Grant`, `Revoke`, `Grants`, `AuthorizeRow`
  and the narrowing predicates are the module's.
- **Breaking: what the store read from magus is an option.** `WithActor`, `WithOrigin`,
  `WithDenials`, `WithWriteHook`, `WithRetention`, `WithSweep`, `WithNotices`,
  `WithClock`, `WithMigrations` and their siblings. Refusals are typed, and magus adds the
  MGS codes and remedies.
- **`internal/job.Store` embeds the module store.** It keeps grading, validation, the
  sweep, checkouts, landings and outcomes, and `NewStore(Location)` is unchanged.
- **Breaking: jobs.jsonl and attempts.jsonl lines carry `safetyscissors.job/v1alpha1`.** A
  store written with `batch.magus/v1alpha1` is refused, saying to move it aside and fork
  the jobs again.
- **Breaking for SDK callers: `internal/job.SetCheckGrader` is gone.** A store grades a
  declared check with `job.Location.Grader`; with none, the fork reports one MGS3039
  problem and an offered gate postcondition is offered ungraded, never passed.
