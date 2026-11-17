### Changed

- **Breaking for SDK callers: `internal/job` no longer forwards the module's names.**
  `job.Actor`, `RefusedError`, `ReadBoundary`, `MaxJobEntries`, `RecordAPIVersion`,
  `Narrowed` and `OpenEntry` are gone from it; name them in `libs/safetyscissors/job`.
- **Breaking for SDK callers: `job.RecordAPIVersion` is gone for `APIVersion`.** It is a
  typed `apiversion.APIVersion` constant.
- **Breaking for SDK callers: magus-only helpers left the module for `internal/job`.**
  `EntryAdvice`, `Entries`, `ParsePauseUntil`, `ReleasedUnderPattern`, `OwedNoSeat` and
  `OwedRemoteCI` moved, and `*RecordVersionError` no longer carries magus's fix.
- **Breaking for SDK callers: the module unexported what only it reads.** `Store.View`,
  `ValidateID`, `WidensInPlace`, `Features`, `States`, `GoalKinds`, `GoalExpects`,
  `MaxUnattributedWrites` and `IDError` are gone from its API.
- **A check renders once.** `CheckCommand` joins the check's argv, led by the binary as
  invoked.
- **A rewrite that drops an unfinished line is a store notice.** attempts.jsonl,
  grants.jsonl and grades.jsonl refused the write.
