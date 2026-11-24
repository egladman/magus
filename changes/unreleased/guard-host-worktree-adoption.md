### Changed

- **A writer subagent can work in its host's own worktree.** `magus job exec`
  in another linked checkout than the job's fork adopts it and says so.
  `spawn-unisolated-row` refuses host isolation only for a fork-made
  `--checkout`, and `sibling-checkout` admits a line's own lease into its
  live job's checkout.
