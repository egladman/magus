### Changed

- **Breaking: a host callback is a Go func type.** An Impl declares its callback
  parameter as `func(ctx context.Context, path string, isDir bool) (types.WalkStep, error)`,
  and the generator renders the Buzz `fun (...)` type from it, labeled with the Go
  parameter names. `std.Callback`, `TypeFunc` and `Arg.Func` are deleted, and
  `internal/interp/bindings/gen/callbacks.go` holds one adapter per callback type.
- **Breaking: a callback's result is decoded, not tested for truthiness.** A charm
  `*_func` predicate or an `fs\watch` callback that returns anything but a bool, or an
  `fs\walk` callback that returns no `WalkStep` case, now raises naming what it
  accepts.
