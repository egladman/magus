### Changed

- **A hook judges from a policy snapshot instead of loading the policy.** One complete load
  of a workspace's guard policy is kept under the user cache, sealed and keyed by the build,
  the checked-out commit and the bytes of every file the load reads. A hook whose key matches
  takes the shell rules, the compiled rules' settings and the approved side from it with no
  VCS status, and loads a function rule only when it is called. `agent harness install`
  writes the snapshot; a hook that misses writes it, and one whose deadline cut the load short
  finishes it after its verdict, within the host's hook timeout less a second. The trail row
  names the phase `policy_snapshot_hit`, `policy_snapshot_miss` or
  `policy_snapshot_unavailable`.
