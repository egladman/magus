### Changed

- **The session store's core lives in `libs/safetyscissors/session`.** Invocation ids'
  file rule, the append-only record writer and reader, the fold and its per-kind cache,
  agent-event loading and pruning moved out of `internal/sessions`, opened on a directory
  the caller names: `Record`, `InvocationStart`, `TargetResult`, `Writer`, `Open`, `Fold`,
  `ReadAll`, `Summary`, `Summarize`, `AgentEvent`, `LoadEvent`, `LoadEvents`,
  `EachAgentEvent`, `ValidFileID` and their kinds and constants keep their names under
  `session`. `ReadKind` and `ReadAgentEvents` are `FoldKind` and `FoldAgentEvents`,
  since they rewrite the fold cache, which is now mode 0600 and refuses a kind that
  cannot name a file. `EventKinds` is a function, `DefaultMaxFiles` is unexported, and
  `Prune` returns the errors it used to drop. The module takes its clock from
  `WithClock`, returns an `*InvalidIDError` for an id that cannot name a file, and reads
  one invocation's file with `ReadInvocation`, which refuses such an id with the same
  error. A failed store operation is a `*session.StoreError`. Breaking for `magus session
  -o json` readers: an invocation's model and host-version pair is `Summary.Model`, keyed
  `model`, not `agent`, which the inlined origin's subagent id already used, so both
  builds of encoding/json dropped one of the two. magus keeps `sessions.Dir`, `NewID`, `ReadGateResults`,
  attention, gates, checkpoints, advice, contact, the prompt cache and the adapters, and
  every magus writer prunes through `sessions.AttentionTie` so a request is never split.
  No alias remains. The store's files and records are unchanged, except that a stored
  target result now omits a zero `duration_ms` and a false `replayed`, which every reader
  already reads as zero.
