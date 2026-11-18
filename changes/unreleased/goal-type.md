### Changed

- **Breaking: a job postcondition is `types.Postcondition`, and stored rows no longer read `completion_gates`.**
  `types.Postcondition`, `GateKind` and `GateExpect` are now `types.Postcondition`, `PostconditionKind` and
  `PostconditionExpect`. The job store neither reads nor writes `completion_gates`, so a row that
  carries only that key reads back with no postconditions; declare them again under `postconditions`.
