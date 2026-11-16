### Changed

- **Breaking for SDK callers: the trail event, its readers and the guard trail's records
  live in `libs/safetyscissors/trail`.** `trail.Event`, `Phase`, `Kind` and its values,
  `OutcomeOK`, `OutcomeError`, `ReadRecent`, `ReadBlob` and `ValidRef` moved out of
  `internal/trail`. The guard trail's records, `Section`, `Verdict`, `Trail`,
  `ReadOptions`, `Observation`, `Spawn` and `Mark`, stay in `types/trail`, since only
  magus's Buzz API makes or reads them. `Sections` and `Verdicts` are functions, their
  values are `SectionRefused`, `VerdictFine` and the rest, and an unknown one is a
  `*trail.UnknownValueError`. `ReadRecent` refuses an empty directory or a limit below 1
  with a `*trail.InvalidReadError` instead of answering nothing, and a missing trail is
  an empty slice. `KindCredentialGrant` is `KindGrantIssued`, with the same
  `credential_grant` on disk. A refused ref is a `*trail.InvalidRefError`. The package
  exports no writer: magus's own writer stays in `internal/trail` until it moves into the
  module with the guard. No alias remains; change the import and the name. Buzz names
  and the files on disk are unchanged.
