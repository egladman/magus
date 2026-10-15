### Changed

- **Breaking for SDK callers: `std`'s job, graph and trail doors take typed options, and
  `std` is in the Go API lock.** `MagusPutJob` takes a `types.JobPut`, `MagusExitJob` a
  `types.JobResult`, `MagusWaitJob` a `*types.JobResult` (nil selects the evidence the
  exit filed), `MagusQuery` a `types.KnowledgeQueryOptions`, `MagusRefs` a
  `types.KnowledgeRefsOptions` and `MagusTrailRead` a `types.TrailReadOptions`, in place
  of `map[string]any`. `std.RegisterJobGrader` is gone: the composition root installs the
  check grader with `job.SetCheckGrader`, and a job store opened without a grader of its
  own grades with it. Buzz is unchanged: `magus\job.put`, `exit`, `wait`, `magus\query`,
  `refs` and `trail.read` take the same maps and refuse the same mistakes, now decoded at
  the binding.
