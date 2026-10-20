### Changed

- **Breaking for SDK callers: the guard rule loader left the root package.** `GuardRules`, `LoadGuardRules`, `LoadApproved*`, `GuardProfile`, `GuardBuiltins`, `GuardPolicy`, `ApprovedPolicy` and the `Magus` methods `SpawnRule`, `CommandRule`, `ShellRules`, `ApprovedSpawnRule`, `ApprovedCommandRule`, `GuardPolicy` and `ApprovedContentIDs` are gone; ask the guard with `JudgeCommand` or `JudgeWrite`.
