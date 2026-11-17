### Changed

- **Breaking: the `libs/safetyscissors/session` read and write functions and `trail.ReadRecent` take a `context.Context` first.**
  `session.Open`, `(*Writer).Append`, `ReadAll`, `ReadInvocation`, `FoldKind`,
  `FoldAgentEvents` and `Prune`, and `trail.ReadRecent`, stop early and return `ctx.Err()`
  as is once the context is done; the multi-file ones check it between files. An SDK caller
  adds the argument at each call.
