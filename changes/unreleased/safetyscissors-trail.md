### Changed

- **Breaking for SDK callers: the trail event, its readers and the guard trail's records
  live in `libs/safetyscissors/trail`.** `trail.Event`, `Phase`, `Kind` and its values,
  `OutcomeOK`, `OutcomeError`, `ReadRecent`, `ReadBlob` and `ValidRef` moved out of
  `internal/trail`, and `Section`, `Verdict`, `Trail`, `ReadOptions`, `Observation`,
  `Spawn` and `Mark` moved out of `types/trail`, which is gone. `Sections` and `Verdicts`
  are functions. `KindCredentialGrant` is `KindGrantIssued`, with the same
  `credential_grant` on disk. A refused ref is a `*trail.InvalidRefError`. The package
  exports no writer: magus's own writer stays in `internal/trail` until it moves into the
  module with the guard. No alias remains; change the import and the name. Buzz names
  and the files on disk are unchanged.
