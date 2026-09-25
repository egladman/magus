### Changed

- **The guard hook asks version control only about the files its policy load read.** An
  allowed shell command no longer waits on a whole-tree status or on resolving the
  repository root. `magus.ApprovedSpawnRuleAt`, `ApprovedCommandRuleAt` and
  `ApprovedWriteRuleAt` are renamed `LoadApprovedSpawnRule`, `LoadApprovedCommandRule`
  and `LoadApprovedWriteRule`; `ApprovedPolicy.Pending` takes a scope.
