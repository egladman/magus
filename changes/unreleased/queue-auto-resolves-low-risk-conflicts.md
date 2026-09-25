### Added

- **The merge queue settles low-risk conflicts in files `vcs.auto_resolve` opts in.**
  A conflict settles when both sides made the same change, one side's change holds the
  other's, or both only added lines at one place. Apply recomputes it, the gate still
  runs, and a `resolved` event and the verdict name each file.
