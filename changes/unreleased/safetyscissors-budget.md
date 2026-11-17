### Changed

- **Breaking for SDK callers: the build budget types live in
  `libs/safetyscissors/budget`.** `types.MachineClaim`, `MachineVerdict`,
  `MachineSnapshot`, `MemoryProfile` and `MemorySizing` are `budget.Claim`, `Verdict`,
  `Snapshot`, `Profile` and `Sizing`; `cache.Limiter` and `cache.MachineBudget` are
  `budget.Limiter` and `budget.Machine`. No alias remains.
- **Breaking for SDK callers: `budget` takes options at construction.** `NewMachine(mb,
  slots, WithReserve(r), WithWrappers(f), WithClock(c))` replaces the setters, and
  `Limiter.SetHooks` is `WithPoolEvents(fn)` on `NewLimiter` or a derived limiter.
- **Breaking for SDK callers: `budget` names what it does.** `Machine.Assert` is `Adopt`,
  `BlockedOn` is `MarkWaiting` and `LimiterStats` is `LimiterSnapshot`. `WaitBoundMS` is a
  `time.Duration` `WaitBound`, with JSON keys unchanged.
- **Breaking for SDK callers: budget refusals are typed and say `budget:`.**
  `*CapacityError`, `*CurveLengthError` and `SlotDeadlock`.
