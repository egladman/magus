### Changed

- **Breaking: a job's goal is a postcondition.** A record declares `postconditions`, and
  `goals` is refused as an unknown member. `job.Goal` is `job.Postcondition` and
  `GateAttempt` is `PostconditionAttempt`, with every name built on them. Buzz reads
  `magus\Postcondition`, `magus\PostconditionKind` and `magus\PostconditionExpect`.
  MGS3038's constant is `JobPostconditionInvalid`.
- **Breaking: the job API's `CompletionGate` and `JobGateAttempt` are `Postcondition` and
  `JobPostconditionAttempt`.** `Job.goals` is `postconditions` and `gate_attempts` is
  `postcondition_attempts`; field numbers are unchanged, so only JSON clients break.
- **Breaking: the job store respells postconditions.** Migration 10 writes `goals`,
  `gate_attempts` and an attempt's `gate_id` as `postconditions`, `postcondition_attempts`
  and `postcondition_id`, and migration 11 renames the `signature-goals` feature to
  `signature-postconditions`. A grades.jsonl line spelling `goal` is refused.
- **Breaking for SDK callers: a store the module wrote before keeps `goals`.** No module
  migration respells it; a caller renames the key in its own migration list.
