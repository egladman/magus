### Added

- **`libs/safetyscissors/job` grades a check goal from a run, on a basis named on the
  grade.** `Store.Witness(ctx, id, goal, cmd)` attaches to the caller's unstarted
  `exec.Cmd`, refuses one whose argv or directory is not the goal's check, digests its
  output, and `(*Witness).Close` reads the real exit status; `Store.Attest` records a
  signed `Statement` the store's `Verifier` accepts; `Store.ReportRun` records a `Report`
  the caller signs with `By`. `Store.Grade(ctx, id, refs)` grades each check goal from
  the `RunRef` cited for it, and a basis the goal does not accept is a violation, never
  a pass. Runs and grades are lines of `grades.jsonl` beside the rows. The store takes
  `WithTree`, `WithVerifier` and `WithCheckCommand`, and refuses a grading call it lacks
  one for with a `*GradingUnconfiguredError`; the other refusals are
  `*WitnessRefusedError`, `*GoalError`, `*TreeError`, `*SignatureError`,
  `*StatementError` and `*ReportError`.

### Changed

- **Breaking: a check goal names the bases it accepts.** `job.Goal` gains `accept`
  (`witnessed`, `attested`, `reported`), and a check goal accepting no basis, or an
  unknown one, is a `JobGoalInvalid` problem. magus resolves a declared check goal that
  names none to `[witnessed]`, and the job store's next migration writes
  `accept: [witnessed]` onto every stored check goal once, by rewriting the file. magus
  still grades its gates from the output store and calls none of the new methods.
