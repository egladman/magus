### Changed

- **A hook judges from a policy snapshot, not a fresh load.** One complete load, sealed and keyed
  by build, commit and every file it reads, is kept under the user cache. A matching hook takes
  the shell rules, compiled settings and approved side from it, and loads function rules only
  when called. `agent harness install` writes it.
- **A hook that misses writes the snapshot, and a deadline-cut load finishes after its verdict.**
  It finishes within the host's hook timeout less a second. The trail row names the phase
  `policy_snapshot_hit`, `policy_snapshot_miss` or `policy_snapshot_unavailable`.
