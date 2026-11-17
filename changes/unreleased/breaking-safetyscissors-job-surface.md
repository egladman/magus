### Changed

- **Breaking for SDK callers: `internal/job` no longer forwards the module's names.**
  `job.Actor`, `job.RefusedError`, `job.ReadBoundary`, `job.MaxJobEntries`,
  `job.RecordAPIVersion`, `job.Narrowed`, `job.Entries` and `job.OpenEntry` in
  `internal/job` are gone; name them in `libs/safetyscissors/job` (`Entries` lives in
  `internal/job` itself, below).
- **Breaking for SDK callers: `libs/safetyscissors/job.RecordAPIVersion` is gone** for
  `APIVersion`, now a typed `apiversion.APIVersion` constant. A caller with its own
  version type converts it, as magus does with `types.APIVersion(job.APIVersion)`.
- **Breaking for SDK callers: magus-only helpers left the module for `internal/job`.**
  `EntryAdvice`, `Entries`, `ParsePauseUntil` with `PauseUntilError`,
  `ReleasedUnderPattern`, `OwedNoSeat` and `OwedRemoteCI` moved; `StaleStoreFix` and
  `DigestSpans` became unexported there. `*RecordVersionError` no longer carries the fix
  in its text: magus adds it.
- **Breaking for SDK callers: the module unexported what only it reads.** `Store.View` is
  gone (`Store.As` still derives a view), and `ValidateID`, `WidensInPlace`, `Features`,
  `States`, `GoalKinds`, `GoalExpects` and `MaxUnattributedWrites` are unexported, and so
  is `IDError`: a refused job id is an error whose text names the rule it breaks.
- A check renders once: `CheckCommand` is the check's argv joined, led by the binary as
  invoked, where it used to spell `magus` while the run spelled the invoked binary.
- attempts.jsonl, grants.jsonl and grades.jsonl report a rewrite that dropped a line no
  writer finished as a store notice, where they refused the write.
