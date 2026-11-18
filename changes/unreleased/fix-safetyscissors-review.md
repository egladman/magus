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
