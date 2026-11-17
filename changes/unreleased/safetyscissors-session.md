### Changed

- **Breaking for SDK callers: the session store's core lives in
  `libs/safetyscissors/session`.** The record writer and reader, the fold and its cache,
  agent-event loading and pruning moved out of `internal/sessions`, opened on a directory
  the caller names. `Record`, `Open`, `Fold`, `ReadAll`, `Summary` and their kin keep
  their names.
- **Breaking for SDK callers: the fold readers say they rewrite the cache.** `ReadKind` and
  `ReadAgentEvents` are `FoldKind` and `FoldAgentEvents`; the cache is mode 0600.
  `EventKinds` is a function, and `Prune` returns the errors it dropped.
- **Breaking for SDK callers: session refusals are typed.** An id that cannot name a file
  is an `*InvalidIDError`, from `ReadInvocation` too, and a failed store operation is a
  `*session.StoreError`. The clock is `WithClock`.
- **Breaking: `magus session -o json` keys an invocation's model `model`, not `agent`.**
  The inlined origin's subagent id already used `agent`, so one of the two was dropped.
- **magus keeps the session store's workspace half.** `sessions.Dir`, `NewID`, attention,
  gates, checkpoints, advice, the prompt cache and the adapters stay, and every writer
  prunes through `sessions.AttentionTie`.
