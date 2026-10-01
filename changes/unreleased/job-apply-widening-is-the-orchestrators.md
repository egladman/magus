### Added

- **Only the orchestrator widens a job through `magus job apply`.** A holder may apply
  only the release of its own paths. A path dropped from a taken job is recorded as a
  revoked release with its digest, and the holder's next write there is refused naming the
  revocation. A refusal never serves a widening command.
