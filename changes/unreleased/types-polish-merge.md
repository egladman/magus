### Changed

- **Breaking for SDK callers: `types.JobStatus.Risks` is `UnresolvedRisks` with a `Clean` method.** `risk.MergeByClaims` takes a `risk.ClaimSide` per side and returns a `types.MergeResolution`.
