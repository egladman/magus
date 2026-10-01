### Added

- **Guard requests carry the caller's subagent id.** `CommandRequest`, `WriteRequest`
  and `SpawnRequest` gain `agent`, the id the host's hook payload names a subagent by,
  empty for the session's main agent. A policy can now tell a subagent from the main
  agent even when magus never saw it spawned and it holds no lease.
- **`vcs\ref`, `vcs\changedFiles` and `vcs\base` take an optional `dir`.** Each reads
  the repository holding that directory, with whichever VCS it uses, instead of the one
  holding the cwd. A `dir` that does not exist raises.

### Changed

- **BREAKING: `vcs\base` is declared as raising.** It raises only for a `dir` that does
  not exist, but a caller outside a raising function must now catch it.
