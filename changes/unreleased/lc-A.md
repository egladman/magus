### Changed

- **Breaking for SDK callers: a job's check is opaque JSON.** `Job`, `Goal`,
  `Declaration`, `Put`, `Statement` and `Report` carry `Check` as `codec.RawMessage`, and
  `LeaseCheck`, `ParseLeaseCheck`, `CheckError` and `SameLeaseCheck` are gone. `Open`
  takes `WithCheckSpec(validate, equal, render)`; without it every row write and grading
  call refuses.
- **Breaking for SDK callers: `ChangedFields`, `SpecChanges` and `AuthorizeRow` take a
  `CheckEqual`.** A goal's check is compared through it, so a bound worker that changes
  any part of a goal's check changes its goals.
- **Breaking: job store records are snake_case.** jobs.jsonl, attempts.jsonl and
  grades.jsonl key every member in snake_case and store enum values as their Go values.
  A file an older build wrote is refused until a migration brings it forward.
- **Breaking for SDK callers: `apiversion` keys `api_version`.** `TypeMeta` and
  `Condition` write `api_version`, `observed_generation` and `last_transition_time`.
