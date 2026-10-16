### Added

- **A `harness_models` row can carry the short name its host takes for the model.** The Claude Code rows declare `"alias"` as `opus`, `sonnet` and `haiku`, so a spawn naming one resolves its model tier by exact match without a workspace declaration. SDK callers see it as `types.ModelPricing.Alias`. An alias on a row with no tier, or on two rows, is an error.
