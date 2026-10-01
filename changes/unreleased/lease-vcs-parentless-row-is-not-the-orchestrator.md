### Fixed

- **`lease-vcs` graded a parentless row as the orchestrator's.** A worker forked without
  `--parent` could push. A lease now counts as a worker when its row has a parent, when
  a subagent holds it, or when its caller names no session. Only a root session holding
  a parentless row is the root.
