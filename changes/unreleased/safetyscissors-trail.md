### Changed

- **Breaking for SDK callers: the trail event and its readers live in
  `libs/safetyscissors/trail`.** `trail.Event`, `Phase`, `Kind`, `OutcomeOK`,
  `OutcomeError`, `ReadRecent`, `ReadBlob` and `ValidRef` moved out of `internal/trail`.
  The guard trail's records stay in `types/trail`. Files on disk are unchanged.
- **Breaking for SDK callers: trail refusals are typed.** `ReadRecent` refuses an empty
  directory or a limit below 1 with a `*trail.InvalidReadError`, a refused ref is a
  `*trail.InvalidRefError`, and an unknown section or verdict a `*trail.UnknownValueError`.
- **Breaking for SDK callers: the module's `trail.Event` reads only `duration_ms`.** magus
  decodes its own older `dur_ms` events in `internal/trail`. `KindCredentialGrant` is
  `KindGrantIssued`, with the same value on disk.
- **The trail readers return what they cannot read.** `trail.ForSession`, `Consulted`,
  `ObservedCounts` and the review's replay answer a store read error instead of an empty
  result, and `SessionTrailHere` reports absence as `false`.
