### Added

- **Guard requests carry the caller's subagent id.** `CommandRequest`, `WriteRequest`
  and `SpawnRequest` gain `agent`, the id the host's hook payload names a subagent by,
  empty for the session's main agent. A policy can now tell a subagent from the main
  agent even when magus never saw it spawned and it holds no lease.
