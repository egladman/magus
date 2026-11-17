### Added

- **`libs/safetyscissors/job` grades a check goal from a run, on a basis named on the
  grade.** `Store.Witness` runs the goal's check, `Store.Attest` records a signed
  `Statement`, and `Store.ReportRun` records a signed `Report`. `Store.Grade` grades each
  check goal from its cited `RunRef`.
- **A basis the goal does not accept is a violation, never a pass.** Runs and grades are
  lines of `grades.jsonl`. The store needs `WithTree`, `WithVerifier` and
  `WithCheckCommand`, and a grading call lacking one is a `*GradingUnconfiguredError`.
