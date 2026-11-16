### Changed

- **Breaking for SDK callers: the model tier set lives in `libs/safetyscissors/tier`.**
  `agent.ModelTier`, `ModelTierSet`, `ModelTierDeclaration`, `ModelTierBounds`,
  `NewModelTierSet`, `RenamedModelTier`, `JobModelTier`, `ModelTierSource` and its cases,
  and `SkillVariant` are `tier.Tier`, `Set`, `Declaration`, `Bounds`, `NewSet`, `Renamed`,
  `Assignment`, `Source` (`SourceJob` and so
  on) and `SkillVariant`. `tier.Renamed` no longer logs; its caller says the rename. The
  tier refusals are typed: `*tier.DeclarationError`, `*UnknownTierError`, `*CompareError`,
  `*RanksAboveError`, `*ForeignTierError` and `*UnmappedHostError`. `ModelTierState` and
  its context helpers stay in magus as `model.TierState`, `model.WithTierState` and
  `model.TierStateFromContext`, and `Tiers` answers `*model.TiersOffError` where it
  answered a nil set. Prices are primitives in `libs/safetyscissors/budget`:
  `types.PriceStep` and `USDPerMTok` are `budget.PriceStep` and `budget.USDPerMTok`, a
  model's rates are `budget.Price` (`PriceMultiplier` is `Price.Multiplier`), and
  `budget.Pricer` answers them. `types.ModelPricing` is `model.Row`, embedding
  `budget.Price`; `model.Table`, which is a `budget.Pricer`, and `InEffect` stay in magus,
  and `InEffect` now overlays an override's tier and alias. `magus.TierCeiling` is `tier.Ceiling`: pass
  `magus.WithSpawnRule(tier.Ceiling)`. It refuses with `*tier.AboveParentError` or
  `*tier.IncomparableError`, and a fork refused through the job store still carries MGS3055
  or MGS3056. No alias remains; change the import and the name. Buzz names and the JSON
  records are unchanged.
