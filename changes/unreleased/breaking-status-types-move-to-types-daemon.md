### Changed

- **Breaking: the daemon status records move to `types/daemon`.** `types.StatusSnapshot`
  is `daemon.Snapshot`, `types.StatusTargetRun` is `daemon.TargetRun`,
  `types.ReadinessStatus` is `daemon.Readiness`, and the rest of `types/status.go` follows,
  dropping the `Status` prefix or suffix. Buzz names and the JSON and wire shapes are
  unchanged.
