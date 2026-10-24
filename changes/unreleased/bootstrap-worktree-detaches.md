### Changed

- **`bootstrap-worktree` detaches a clean worktree onto the job's checkpoint.** When the
  base the lease recorded diverged from the checkpoint and the tracked files are clean, the
  script runs `git switch --detach <checkpoint>`, prints that it did, retakes the lease and
  continues. A worktree with tracked edits is still refused.
