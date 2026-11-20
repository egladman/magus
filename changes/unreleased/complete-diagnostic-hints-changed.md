### Changed

- **Breaking for SDK callers: check and job fault remedies are hints.**
  `check.Check.Hint` and `job.Fault.Hint` replace their `Fix` fields. Doctor check
  JSON and YAML use `hint` for the suggested command.
