### Changed

- **Breaking for SDK callers: guard verdicts live in `libs/safetyscissors/verdict`.**
  `types.GuardDecision`, `GuardAdvise`, `GuardDeny`, `GuardVerdict`, `StricterGuardVerdict`,
  `ValidateGuardRuleName`, `GuardJudgment` and `GuardDeniedError` are `verdict.Decision`,
  `Advise`, `Deny`, `Verdict`, `Stricter`, `ValidateRuleName`, `Judgment` and
  `DeniedError`.
- **Breaking for SDK callers: steps live in `libs/safetyscissors/step`.** `agent.Step`,
  `StepSpec`, `StepRequest` and `StepAPIVersion` are `step.Step`, `Spec`, `Request` and
  `APIVersion`. A judgment's remedies are `verdict.NextStep`, and its `LeaseFrom` is a
  string. No alias remains.
