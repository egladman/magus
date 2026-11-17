### Changed

- **Breaking: the job row store lives in `libs/safetyscissors/job`.** `job.Open(dir, opts...)`
  opens jobs.jsonl, attempts.jsonl, grants.jsonl and the lock in the directory the caller
  names; magus passes the one `vcs.StateDir` computes, so rows stay where they were. The
  store's write pipeline moved with it: `Update`, `Mutate`, `MutateSeeing`, `Rewrite`,
  `Read`, `List`, `Unswept`, `Enter`, `ConsumeEntry`, `RecordUnattributedWrite`, `Clear`,
  `Delete`, `Attempts`, `ChangeLiveAttempt`, `AppendAttempts`, `Grant`, `Revoke`,
  `Grants`, `LiveGrant`, the authorization core (`Actor`, `RefusedError`, `Grading` and its
  `AsDeclaration` to `AsAdvance`, `AuthorizeRow`, `ChangedFields`, `SpecChanges`,
  `Subset`, `ReadBoundary`) and the narrowing predicates (`Covers`, `CoversPath`,
  `Matching`, `Narrowed`, `ReleasedReach`, `ClaimsOnFile`, `FileRelease`). Everything that read magus is an option: `WithActor`,
  `WithOrigin`, `WithDenials`, `WithWriteHook` (stages `StageAuthorize`, `StageRefork`,
  `StageEnd`, `StageTier`, `StagePlaced`, `StageDelete`), `WithRetention`, `WithSweep`,
  `WithNotices`, `WithClock`, `WithDeclarationDigest`, `WithHead`, `WithMigrations`,
  `WithCredentialKinds` and `WithErrorWrap`. The store's refusals are typed:
  `*NewerWriterError`, `*NewerRecordsError`, `*RecordVersionError`,
  `*LegacyDocumentError`, `*GrantRefusedError` and `codec.*LineError`; magus adds the MGS
  codes and remedies, the stale-store fix among them.
- `internal/job.Store` embeds the module store and keeps grading, validation, the sweep,
  checkouts, landings and outcomes; `NewStore(Location)` is unchanged.
- **Breaking: jobs.jsonl and attempts.jsonl lines carry `safetyscissors.job/v1alpha1`**, and
  the requires annotation is `safetyscissors.job/requires`. A store written with
  `batch.magus/v1alpha1` is refused naming the file and saying to move it aside and fork
  the jobs again, as the API groups note says of every record.
- **Breaking for SDK callers: `internal/job.SetCheckGrader` is gone**, and with it
  `std.RegisterJobGrader` (see its own entry). A store grades a declared check with
  `job.Location.Grader`; cmd/magus, the server and magus\workspace().putJob pass one, the
  last through the new `magus.Magus.CheckGrader`. `job.UnrunGates` and `job.RowNotesWith`
  take the grader. A store whose Location names no grader grades no declared check.
