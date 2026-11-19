### Changed

- **The `hack/policy` helpers return errors as values.** `mandate`, `pr_workflow`,
  `version-bumps`, `stale` and `proofread` return `result\Result::<T>` below the functions
  the host or another script calls, and their `no-throw` rows fall in `analyzers.yaml`.
