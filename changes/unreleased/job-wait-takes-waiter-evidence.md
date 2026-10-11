### Added

- **`magus job wait --evidence <goal>=<ref>` grades a check goal against a run from your own checkout.**
  For a goal that failed where the work was written but passes here. The ref resolves in this
  checkout only. The files in the job's diff must match in both checkouts and must not change
  here after the run. The job records `waiter_evidence`.
