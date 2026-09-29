### Added

- **`magus job edit` widens or revokes a live job's write paths.** `--add-write-path`
  and `--remove-write-path` merge into the row in one write that keeps its state; it
  previews until `--apply`. A revoked path is recorded as a release with its digest,
  and the holder's next write there is refused naming the revocation. A refusal no
  longer serves a widening command.
