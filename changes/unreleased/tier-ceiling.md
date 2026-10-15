### Added

- **A child job never outranks its parent's model tier.** `magus job fork` refuses a row
  whose tier ranks above the tier its parent row was forked at, by position in your
  model-tier declaration, whichever step chose it: the row, the matrix or the default. The
  refusal names both tiers, the `--model` that fits, and the way up: a person forks the
  higher-tier row.
- **`spawn-above-parent-tier` refuses a spawn above its spawner's tier.** The root session
  runs on the model the person picked; a spawn naming a model whose tier ranks above it is
  refused once the host has reported the session's model and thread, and advised when the
  spawner's model is inferred, such as a subagent spawned on no model of its own. It denies
  by default; set it to advise or off in `magus\guard.builtins`. A tier is read from an
  exact alias only: a model that is none, such as a transcript's full model id, has an
  unknown tier, and the rule advises naming the model and the aliases your declaration maps.
- **`ModelTier.Within` reports a tier above its ceiling** as an error naming both tiers,
  for a Go caller holding a child's tier to its parent's.
