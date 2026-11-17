### Changed

- **Breaking for SDK callers: `budget.Limiter.Yield` refuses a context holding no slots.**
  It returned after releasing one slot it guessed the caller held; it now returns a
  `*budget.NoSlotsHeldError` without running fn. Mark the context with `Admit`, or
  `WithSlotsHeld` after a `Seat` or a raw `Acquire`, or check `SlotHeld` first.
- **Breaking: `budget.Limiter.ReleaseN` frees no more slots than are running.** The excess
  is left unfreed and reported as the new `PoolEvent.Shortfall`, where it panicked in the
  semaphore. A count below one is floored to one, as `AcquireN` floors it.
- **Breaking: `budget.WithDeadlockGrace` and `budget.GraceError` are removed.** Nothing
  outside the module's tests set the grace; a limiter waits out `DefaultDeadlockGrace`.
- **Breaking: `tier.Set.Bounds` and `tier.Set.Skills` return `(value, error)`.** A tier
  from another set, or the zero value, is a `*tier.ForeignTierError` where it read as no
  caps and no skill variant.
- **Breaking: `tier.Renamed` is removed and `tier.Set.Parse` reads only the declared
  spelling.** The old shipped names economy, standard and principal are read as routine,
  bounded and design only on a harness spell's model rows, which warn of the rename. A
  job row, a matrix answer or a `job fork --model` naming an old tier is now an unknown
  tier.
- **Breaking: `session.ReadInvocation` refuses a file as `ReadAll` refuses the store.** A
  retired group is a `*session.RetiredGroupError`, a retired guard decision a
  `*session.RetiredDecisionError`, and a file that cannot be opened or read to its end a
  `*session.StoreError`, where each read as an invocation with fewer records. The two
  retired errors state the fact and name no remedy. `session.TargetResult` omits a zero
  `duration_ms` and a false `replayed`.
- **Breaking: a BestEffort sandbox policy says when it runs a child unconfined.**
  `sandbox.New` refuses `WithMode(BestEffort)` without the new `WithUnconfined(func(error))`,
  which hears each child `Command` starts unconfined, with a `*sandbox.UnconfinedError`.
  `Policy.Confines` returns that error under BestEffort too, with `UnconfinedError.Mode`
  saying which mode it is. New refusals are a `*sandbox.OptionError`.
- **Breaking: the Linux launcher's argv[0] is an option.** `sandbox.WithLauncherName` names
  it for both `Command` and `Relaunch`, which now takes options; the default is
  `safetyscissors-sandbox-launch`, where it was `magus-sandbox-launch`. magus passes
  `magus-sandbox-launch`.
- **The macOS seatbelt base lets a confined process start.** It reads `/System`,
  `/private/var/db/dyld`, `/dev/urandom`, `/dev/tty` and `/dev/fd`, and reads and writes
  `/dev/null`. An exec of a symlink is granted when an exec rule covers its target.
- **`sandbox/filesystem` errors start with `filesystem:`.**
- **Breaking: `libs/secrets` refuses an endpoint when `http.DefaultTransport` is not an
  `*http.Transport`,** with a `*secrets.TransportError`, where it forwarded through that
  transport with its proxy and no first-byte timeout.
