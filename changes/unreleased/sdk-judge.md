### Added

- **SDK callers can ask the guard.** `Magus.JudgeCommand` and `Magus.JudgeWrite` judge a
  shell line or a file write as the hook judges an agent's: the compiled rules, the
  workspace's `magus\guard` rules beside their approved copies, and the session's earlier
  calls. A deny comes back as a `types.GuardJudgment` with its reason and rule; the error
  is only for a judgment that could not be made, such as rules that did not load.
- **`WithSession` names the agent session a run is made for.**

### Changed

- **Breaking for SDK callers: `Run`, `RunCI`, `RunAffected` and `RunGate` are judged before
  they run.** Each is judged as the CLI line that reaches it (`magus run <target>
  <project>`, `magus affected <target>`, `magus affected ci`), and a deny returns a
  `*types.GuardDeniedError` carrying the judgment, with nothing run. The CLI is not judged
  twice: its host's hook already judged the invocation.
- **Breaking for SDK callers: the guard rule loader left the root package.**
  `GuardRules`, `LoadGuardRules`, `LoadApproved*`, `EffectiveGuardProfile`,
  `GuardBuiltins`, `GuardPolicy`, `ApprovedPolicy` and the `Magus` methods `SpawnRule`,
  `CommandRule`, `ShellRules`, `ApprovedSpawnRule`, `ApprovedCommandRule`, `GuardPolicy`
  and `ApprovedContentIDs` are gone; ask the guard with `JudgeCommand` or `JudgeWrite`.
- **A guard rule load in a program that links no Buzz engine is an error.** It returned
  no rules, which judged every call by the compiled rules alone.
