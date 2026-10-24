### Changed

- **A root's refused reset or rebase names the integrator's path.** The
  orchestrator-groundwork refusal of `git reset` or `git rebase` now says how a worker's
  commits get folded: fork an integration job naming the branch in `integrates`, or spawn
  `<root>/integrate <job>` in the job's checkout, which runs `git reset --soft
  <checkpoint>` there and makes one commit.
