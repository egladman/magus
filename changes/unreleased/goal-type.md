### Changed

- **Breaking: a job goal is `types.Goal`, and stored rows no longer read `completion_gates`.**
  `types.CompletionGate`, `GateKind` and `GateExpect` are now `types.Goal`, `GoalKind` and
  `GoalExpect`. The job store neither reads nor writes `completion_gates`, so a row that
  carries only that key reads back with no goals; declare them again under `goals`.
