### Changed

- **Breaking for SDK callers: guard verdicts and steps live in
  `libs/safetyscissors/verdict` and `libs/safetyscissors/step`.** `types.GuardDecision`,
  `GuardAllow`, `GuardAdvise`, `GuardDeny`, `GuardVerdict`, `StricterGuardVerdict`,
  `ValidateGuardRuleName`, `GuardJudgment` and `GuardDeniedError` are
  `verdict.Decision`, `Defer`, `Advise`, `Deny`, `Verdict`, `Stricter`,
  `ValidateRuleName`, `Judgment` and `DeniedError`. `agent.Step`, `StepSpec`,
  `StepRequest` and `StepAPIVersion` are `step.Step`, `Spec`, `Request` and
  `APIVersion`. A judgment's remedies are `verdict.NextStep`, which converts to and from
  `types.NextStep`, and its `LeaseFrom` is a string. No alias remains; change the import
  and the name. `GuardAllow` became `verdict.Defer` with the guard's decision rename; see
  that note for the envelope and the Buzz names.
