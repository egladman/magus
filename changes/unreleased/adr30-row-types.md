### Changed

- **`types.GateWaiver`, `types.WorkItem` and their companions leave the public `types` package.**
  The gate waiver row now lives beside its store in `internal/guard`, and the work ledger row,
  with `WorkKind`, `WorkState`, `HeldRevision` and their constants, in `internal/work`
  (ADR 0030). No Buzz binding, magusfile or script read either type.
