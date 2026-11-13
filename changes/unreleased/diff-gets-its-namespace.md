### Changed

- **Breaking: a check's verdict and a change's risk tier moved to `types/check` and the
  `magus\check` namespace.** In Go, `types.Check`, `types.Evidence` and its four
  constants, `types.Tier`, `types.TierEvidence` and the `Risk*` tiers are
  `check.Check`, `check.Evidence`, `check.Tier`, `check.TierEvidence` and
  `check.RiskTrivial` and so on. `types.CheckStatus` is `check.Status`, and
  `types.CheckOK`, `CheckFail` and `CheckAdvice` are `check.OK`, `check.Fail` and
  `check.Advice`. In Buzz, `Check`, `CheckStatus`, `Evidence`, `Tier` and `TierEvidence`
  are `magus\check\Check`, `magus\check\Status`, `magus\check\Evidence`,
  `magus\check\Tier` and `magus\check\TierEvidence`. A script that names one adds
  `import "magus/check";`. `DoctorReport`, `TierReport` and `TierOptions` stay in
  `types` and `magus`.
- **Breaking: `ImpactCoverage` and `ImpactProject` moved to `types/impact` and the
  `magus\impact` namespace.** In Go they are `impact.Coverage` and `impact.Project`. In
  Buzz they are `magus\impact\Coverage` and `magus\impact\Project`, after
  `import "magus/impact";`. `ImpactResult`, `ImpactSymbol` and `ImpactFileCoverage`
  stay in `types` and `magus`.
