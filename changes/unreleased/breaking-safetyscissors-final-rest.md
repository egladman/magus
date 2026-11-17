### Changed

- **Breaking for SDK callers: `budget.Limiter.Yield` refuses a context holding no slots.**
  It returns a `*budget.NoSlotsHeldError` without running fn. Mark the context with
  `Admit`, or `WithSlotsHeld` after a `Seat` or `Acquire`.
- **Breaking: `budget.Limiter.ReleaseN` frees no more slots than are running.** The excess
  is reported as `PoolEvent.Shortfall`, where it panicked, and a count below one is
  floored to one.
- **Breaking: `budget.WithDeadlockGrace` and `budget.GraceError` are removed.** A limiter
  waits out `DefaultDeadlockGrace`.
- **Breaking: `tier.Set.Bounds` and `tier.Set.Skills` return `(value, error)`.** A foreign
  or zero tier is a `*tier.ForeignTierError`.
- **Breaking: `tier.Renamed` is removed and `tier.Set.Parse` reads only declared names.**
  economy, standard and principal read as routine, bounded and design only on a harness
  spell's model rows, which warn of the rename.
- **Breaking: `session.ReadInvocation` refuses a file as `ReadAll` refuses the store.** A
  retired group, a retired decision or an unreadable file is its typed error, where each
  read as fewer records. `TargetResult` omits a zero `duration_ms` and a false `replayed`.
- **Breaking: a BestEffort sandbox policy says when it runs a child unconfined.**
  `sandbox.New` refuses BestEffort without `WithUnconfined(func(context.Context, error))`,
  which hears each unconfined child with `Command`'s ctx. Refusals are a
  `*sandbox.OptionError`.
- **Breaking: the Linux launcher's argv[0] is `sandbox.WithLauncherName`.** It defaults to
  `safetyscissors-sandbox-launch`; magus passes `magus-sandbox-launch`, and `Relaunch`
  takes options.
- **The macOS seatbelt base lets a confined process start.** It reads `/System`, the dyld
  cache and the standard devices, and grants exec of a symlink whose target an exec rule
  covers.
- **`sandbox/filesystem` errors start with `filesystem:`.**
- **Breaking: `libs/secrets` refuses an endpoint over a non-`*http.Transport` default.** It
  answers a `*secrets.TransportError` where it forwarded with no first-byte timeout.
