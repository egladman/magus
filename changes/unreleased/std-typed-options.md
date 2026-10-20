### Changed

- **Breaking for SDK callers: `std`'s job, graph and trail doors take typed options.** `MagusPutJob` takes a `types.JobPut`, `MagusExitJob` a `types.JobResult`, `MagusWaitJob` a `*types.JobResult` (nil selects the evidence the exit filed), `MagusQuery` a `types.KnowledgeQueryOptions`, `MagusRefs` a `types.KnowledgeRefsOptions` and `MagusTrailRead` a `types.TrailReadOptions`, in place of `map[string]any`.
