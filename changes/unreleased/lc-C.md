### Changed

- **Breaking: the job store migrates to snake_case.** Migration 10 respells every
  jobs.jsonl line, header included, and every attempts.jsonl line: camelCase keys through
  an explicit table, CamelCase enum values back to their Go values, and
  `noDefaultCharms` inside each check. A value no enum lists is refused and the store is
  left as it was. A magus from before it refuses the migrated store as newer.
- A check whose project is the workspace root renders as `magus run <target> --all`.
- `types.LeaseCheck` and the `types.Job`, `types.Goal`, `types.JobList` and
  `types.Declaration` views read the opaque check as a lease check; `-o yaml` prints a
  row's check as an object.
- `ValidateDeclaration`, `ValidateGoal` and `DeclarationProblems` move from `types` to
  `internal/job`, and report a check that does not read as a lease check.

### Fixed

- A bound worker can no longer change the accept list or the signature of its own goal.
