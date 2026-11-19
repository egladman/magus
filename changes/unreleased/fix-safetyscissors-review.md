### Changed

- **Breaking for SDK callers: `job.WithLease` refuses an invalid id.** It returns
  `(context.Context, error)`, and an id failing `ValidID` is an `*InvalidLeaseError`
  where it recorded no lease. `""` and the new `job.WithoutLease` still record none.
  A request naming an invalid lease is refused.
- **Breaking for SDK callers: redaction without a resolver is refused.** `secrets.Redact`
  and `secrets.RedactString`, and the `Resolver` methods on a nil resolver, return
  `(value, error)` and a `*secrets.NoResolverError`, where they returned the text
  unmasked. magus installs a resolver at every entry point, and a journal record that
  cannot be masked is dropped with a `journal.dropped` warning.
- **Breaking for SDK callers: a secret too short to mask is reported on every path.**
  `Resolver.Register` returns an error, a `*secrets.ShortSecretError` for such a value,
  and `Read` refuses one with it when no `WithShortSecret` notice hears it.
- **Breaking for SDK callers: a nil `sandbox.Policy` is refused.** `Check`, `Confines`
  and `sandbox.Command` return a `*sandbox.NilPolicyError`, where a nil policy confined
  nothing and `Command` started the child unconfined.
- **Breaking for SDK callers: `budget.NewLimiter(0)` is no capacity, not unlimited.**
  Every acquire on it is a `*CapacityError`; `budget.NewUnlimitedLimiter` bounds nothing.
  `AcquireN` and `Seat` refuse fewer than one slot with a `*SlotCountError`, where they
  took one.
- **Breaking for SDK callers: `verdict.Decision("").Valid()` is false.** An unset
  decision is not a declared one; `UnmarshalText` still reads `""` as unset.
- **Breaking for SDK callers: `secrets.NewResolver` takes a `Config` and returns an error.**
  A nil option value is a `*secrets.OptionError`. A read with no provider selected is a
  `*ReadError` wrapping `ErrNoFallback`, where it read the environment; set
  `Config.Fallback` to `secrets.EnvProvider{}` to keep that.
- **Breaking for SDK callers: `codec.JSONL` is built by `codec.NewJSONL`.** It takes a
  `JSONLConfig` and options, and its fields are unexported. An empty `Path`, a zero `Perm`
  (it meant 0644) or a nil `Key` is a `*JSONLConfigError`, a nil option value an
  `*OptionError`; the zero `JSONL` is refused by `Load` and `Update`, where it panicked.
- **Breaking for SDK callers: `session.Open` and `session.LoadEvents` take a `Config`.**
  `Retention` and `PruneErrors` are required: opening prunes at `Retention`, where it used
  `DefaultRetention`, and hands each prune, lock or stamp error to `PruneErrors`, where it
  dropped them. A missing field is a `*session.ConfigError`, a nil option an
  `*OptionError`.
- **Breaking for SDK callers: `job.Open` takes a context and a `Config`.** `StoreOption` is
  `job.Option`, and every `With` option refuses a nil or empty value with a
  `*job.OptionError`, where it was stored; `CheckSpecError` is gone. An empty `Dir` is a
  `*job.ConfigError`.
- **Breaking for SDK callers: `sandbox.New` takes a `Config`.** Binds move to
  `Config.Binds`. A nil function, an empty lease id or an unknown mode is a
  `*sandbox.OptionError`, where it was stored.
- **Breaking for SDK callers: `safetyscissors.New` takes a `Config`.** `WithInvocation`,
  `WithActor`, `WithNotices` and `WithCheckSpec` are `Config` fields, joined by the
  required `Retention` and `PruneErrors`; a missing one is a `*ConfigError`.
  `WithJobOptions` takes `job.Option`.
- **`safetyscissors.New` refuses any two stores in one directory.** Jobs, Sessions and
  Activity are compared pairwise after `filepath.Clean`, where only Jobs and Sessions
  were compared, as written.
