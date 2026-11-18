### Changed

- **Breaking for SDK callers: `job.WithLease` refuses an invalid id.** It returns
  `(context.Context, error)`, and an id failing `ValidID` is an `*InvalidLeaseError`
  where it recorded no lease. `""` and the new `job.WithoutLease` still record none.
  A request naming an invalid lease is refused.
