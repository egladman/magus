### Changed

- **Breaking for Buzz scripts and SDK callers: a job status names its risks
  `unresolvedRisks`.** The Buzz `JobStatus` key `risks` is `unresolvedRisks`, matching the
  JSON `unresolved_risks`, and `types.JobStatus.Risks` is `UnresolvedRisks` with a `Clean`
  method. `risk.MergeByClaims` takes a `risk.ClaimSide` per side and returns a
  `types.MergeResolution`.
