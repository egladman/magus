### Changed

- **Breaking for guard rules: a request's `threadKnown` is `threadFrom`, and `context` is
  `window`.** `magus\guard.command`, `magus\guard.write` and `magus\guard.spawn` requests
  carry `threadFrom`, `"reported"` or `"inferred"`, in place of a boolean whose false meant
  inferred, and `window`, a `ContextWindow` (formerly `AgentContext`), in place of
  `context`, which read as the handed prompt. `thread` is never empty: a caller magus
  cannot place is `"unknown"`.
- **Breaking for SDK callers: one `types.AgentFacts` holds a caller's cost facts.** The
  trail's `AgentCommand`, `AgentSpawn` and `SessionCall` and `observability.AgentCall`
  embed it, so the trail's JSON keys are unchanged; `AgentCall.Model` is `ContextModel`.
  Each record gains `thread_from`. `result_chars` is written whenever a result was
  observed, 0 included, and absent when none was, where 0 meant both.
  `DelegableClass` and `ContextSource` are named types.
- **Breaking for SDK callers: `types.GuardProfile` has `Effective` and `Stricter`.** They
  replace `Level` and `StricterProfile`. `String` renders the profile as written, empty
  when unset, as `MarshalText` does.
- **Breaking for SDK callers: `config.Agents.ResolveBilling` returns only the mode.**
- **Breaking for SDK callers: `types.ModelFacts` is `types.ModelPricing`.** Prices are
  `types.USDPerMTok`, `Factor` is `PriceMultiplier`, and `PriceStep` with `StepFactor` is
  `LongContext`, a `*types.PriceStep` that is nil for a model with no step. The
  `agents.models` keys in magus.yaml and the `harness_models` row keys are unchanged.
- **Breaking: an `agents.models` step replaces the harness row's whole step.** An entry
  setting `step_factor` alone, which kept the harness row's `price_step`, is an error;
  give both. A `harness_models` row setting only one of the two is an error too.
- **Breaking: `magus session show -o json` reshapes its cost sections.** `share` and
  `ignored` are no longer encoded, since they follow from the counts beside them. `shadow`
  counts each profile's verdicts under `decisions`, keyed by verdict, in place of
  `allow`, `advise` and `deny`, so a verdict outside the three is counted rather than
  read as an allow. A spawn that declared no model is tallied under `""` rather than
  `"none declared"`, which the text view still prints.

### Removed

- **Breaking: `magus session show` drops the past price step section.** It compared the
  spawning thread's context with the child's price step: the trail records no child's
  context, so the count measured nothing. `past_price_step` leaves the JSON, and
  `trail.SessionSpawn.ContextTokens`, which read the spawner's context as the child's,
  goes with it.
