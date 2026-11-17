### Changed

- **Breaking for SDK callers: the model tier set lives in `libs/safetyscissors/tier`.**
  `agent.ModelTier`, `ModelTierSet`, `ModelTierDeclaration`, `ModelTierBounds`,
  `NewModelTierSet`, `JobModelTier` and `ModelTierSource` are `tier.Tier`, `Set`,
  `Declaration`, `Bounds`, `NewSet`, `Assignment` and `Source`. Buzz names and JSON
  records are unchanged.
- **Breaking for SDK callers: tier refusals are typed.** `*tier.DeclarationError`,
  `*UnknownTierError`, `*CompareError`, `*RanksAboveError`, `*ForeignTierError` and
  `*UnmappedHostError`.
- **Breaking for SDK callers: the tier state stays in magus as `model.TierState`.**
  `Tiers` answers `*model.TiersOffError` where it answered a nil set.
- **Breaking for SDK callers: prices are `libs/safetyscissors/budget` primitives.** A
  model's rates are `budget.Price`, and `types.ModelPricing` is `model.Row`, embedding it.
  `InEffect` overlays an override's tier and alias.
- **Breaking for SDK callers: `magus.TierCeiling` is gone.** Pass
  `magus.WithSpawnRule(tier.Ceiling)`; a refused fork still carries MGS3055 or MGS3056.
