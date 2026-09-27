### Changed

- **The guard judges `git worktree remove` instead of refusing every one.** It
  passes a clean, unlocked worktree other than the session's own, with no live
  job taken there and every commit reachable from a remote-tracking ref, the
  base, or a finished job's filed result. Otherwise the refusal names the
  failed condition; `--force` changes nothing. `jj workspace forget` stays
  refused.
