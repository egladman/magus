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
- **Breaking for SDK callers: a nil `sandbox.Policy` is refused.** `Check`, `Confines`
  and `sandbox.Command` return a `*sandbox.NilPolicyError`, where a nil policy confined
  nothing and `Command` started the child unconfined.
- **Breaking for SDK callers: `budget.NewLimiter(0)` is no capacity, not unlimited.**
  Every acquire on it is a `*CapacityError`; `budget.NewUnlimitedLimiter` bounds nothing.
  `AcquireN` and `Seat` refuse fewer than one slot with a `*SlotCountError`, where they
  took one.
