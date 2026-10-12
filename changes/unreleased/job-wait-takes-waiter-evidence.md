### Added

- **`magus job wait --evidence <goal>=<ref>` grades a check goal against a run from your own checkout.**
  The ref resolves here only. The job's changed files must match in both checkouts and read
  as the run read them: at its revision for a clean tree, else unmodified since it started.
  The job records `waiter_evidence`.
