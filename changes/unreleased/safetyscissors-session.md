### Changed

- **The session store's core lives in `libs/safetyscissors/session`.** Invocation ids'
  file rule, the append-only record writer and reader, the fold and its per-kind cache,
  agent-event loading and pruning moved out of `internal/sessions`, opened on a directory
  the caller names: `Record`, `InvocationStart`, `TargetResult`, `Writer`, `Open`, `Fold`,
  `ReadAll`, `ReadKind`, `ReadAgentEvents`, `Summary`, `Summarize`, `AgentEvent`,
  `LoadEvent`, `LoadEvents`, `EachAgentEvent`, `ValidFileID`, `Prune` and their kinds and
  constants keep their names under `session`. The module takes its clock from `WithClock`,
  returns `ErrInvalidID` for an id that cannot name a file, and reads one invocation's
  file with `ReadInvocation`. magus keeps `sessions.Dir`, `NewID`, `ReadGateResults`,
  attention, gates, checkpoints, advice, contact, the prompt cache and the adapters, and
  every magus writer prunes through `sessions.AttentionTie` so a request is never split.
  No alias remains. The store's files and records are unchanged, except that a stored
  target result now omits a zero `duration_ms` and a false `replayed`, which every reader
  already reads as zero.
