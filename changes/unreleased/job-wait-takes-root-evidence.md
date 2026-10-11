### Added

- **`magus job wait --evidence <goal>=<ref>` grades a check goal against a run from your own checkout.**
  For a goal that failed where the work was written but passes here. The ref resolves in this
  checkout only, the changed paths must read the same in both, and the job records `root_evidence`.
