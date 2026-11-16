### Changed

- **Breaking for SDK callers: the build budget types live in
  `libs/safetyscissors/budget`.** `types.MachineClaim`, `MachineVerdict`,
  `MachineSnapshot`, `MemoryProfile`, `MemorySizing` and their siblings are
  `budget.Claim`, `budget.Verdict`, `budget.Snapshot`, `budget.Profile` and
  `budget.Sizing`; `cache.Limiter` and `cache.MachineBudget` are `budget.Limiter` and
  `budget.Machine`. No alias remains; change the import and the name.
- **Breaking for SDK callers: `budget` takes options and names what it does.**
  `NewMachine(mb, slots).WithReserve(r).WithWrappers(f).WithClock(c)` is
  `NewMachine(mb, slots, WithReserve(r), WithWrappers(f), WithClock(c))`. `Machine.Assert`
  is `Machine.Adopt`, `BlockedOn` is `MarkWaiting` and `LimiterStats` is
  `LimiterSnapshot`. `Limiter.SetHooks` is gone: pass `WithPoolEvents(fn)` to
  `NewLimiter`, or derive a limiter sharing the pool with `Limiter.WithPoolEvents(fn)`.
  `WithDeadlockGrace` returns `(LimiterOption, error)` and refuses a grace that is not
  positive with `*GraceError`. `Claim.WaitBoundMS` and `Admission.WaitBoundMS` are
  `WaitBound`, a `time.Duration`, and every `WaitedMS` is `WaitedMs`; the JSON keys are
  unchanged. `Snapshot.HolderList` is gone. Refusals are typed and say `budget:`:
  `*CapacityError`, `*CurveLengthError` and `SlotDeadlock`.

### Fixed

- A `budget.Limiter.Seat` of fewer than one slot gave back none of the slot it took.
  `Limiter.Yield` released more slots than the pool holds for a context marked with more,
  and dropped a failed re-acquire. A slot-watch grace timer stopped after it fired could
  clear the timer that replaced it.
