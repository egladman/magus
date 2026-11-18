### Added

- **`libs/safetyscissors/job` grades a check postcondition from a run, on a basis named on the
  grade.** `Store.Witness` runs the postcondition's check, `Store.Attest` records a signed
  `Statement`, and `Store.ReportRun` records a signed `Report`. `Store.Grade` grades each
  check postcondition from its cited `RunRef`.
- **A basis the postcondition does not accept is a violation, never a pass.** Runs and grades are
  lines of `grades.jsonl`. The store needs `WithTree`, `WithVerifier` and
  `WithCheckCommand`, and a grading call lacking one is a `*GradingUnconfiguredError`.
