### Changed

- **Breaking for SDK callers: the build budget types live in
  `libs/safetyscissors/budget`.** `types.MachineClaim`, `MachineVerdict`,
  `MachineSnapshot`, `MemoryProfile`, `MemorySizing` and their siblings are
  `budget.Claim`, `budget.Verdict`, `budget.Snapshot`, `budget.Profile` and
  `budget.Sizing`; `cache.Limiter` and `cache.MachineBudget` are `budget.Limiter` and
  `budget.Machine`. No alias remains; change the import and the name.
