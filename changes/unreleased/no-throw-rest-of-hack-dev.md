### Changed

- **The `hack/dev` scripts return a failure instead of throwing it.** Eleven scripts, from
  `bootstrap-worktree` to `split-into-branches`, return one shared `result\Result`; only `main`
  throws, with the failure's message. A host error now reads as its message, where it printed the
  map it crossed as.
