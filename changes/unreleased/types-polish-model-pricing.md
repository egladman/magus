### Changed

- **Breaking for SDK callers: `types.ModelFacts` is `types.ModelPricing`.** Prices are `types.USDPerMTok`, `Factor` is `PriceMultiplier`, and `PriceStep` with `StepFactor` is `LongContext`, a `*types.PriceStep` that is nil for a model with no step. The `agents.models` keys in magus.yaml and the `harness_models` row keys are unchanged.
