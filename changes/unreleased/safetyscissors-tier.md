### Changed

- **Breaking for SDK callers: the model tier set lives in `libs/safetyscissors/tier`.**
  `agent.ModelTier`, `ModelTierSet`, `ModelTierState`, `WithModelTierState`,
  `ModelTierStateFromContext`, `ModelTierDeclaration`, `ModelTierBounds`,
  `NewModelTierSet`, `RenamedModelTier`, `JobModelTier`, `ModelTierSource` and its cases,
  and `SkillVariant` are `tier.Tier`, `Set`, `State`, `WithState`, `StateFromContext`,
  `Declaration`, `Bounds`, `NewSet`, `Renamed`, `Assignment`, `Source` (`SourceJob` and so
  on) and `SkillVariant`. `types.ModelPricing`, `PriceStep` and `USDPerMTok` are
  `tier.Pricing`, `PriceStep` and `USDPerMTok`, and `model.Table` and `InEffect` are
  `tier.Table` and `InEffect`. `magus.TierCeiling` is `tier.Ceiling`: pass
  `magus.WithSpawnRule(tier.Ceiling)`. It refuses with `*tier.AboveParentError` or
  `*tier.IncomparableError`, and a fork refused through the job store still carries MGS3055
  or MGS3056. No alias remains; change the import and the name. Buzz names and the JSON
  records are unchanged.
