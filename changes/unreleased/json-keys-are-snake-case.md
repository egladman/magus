### Changed

- **Breaking: the JSON keys magus owns are snake_case.** The cache manifest, the `dry`
  graph, spell discovery, the spell syntax and version descriptors, `describe` output,
  the figure JSON and `hack/dev/show-import-meet.buzz -o json` respell their camelCase
  keys. External formats keep their spelling. `tagliatelle` holds the rule in lint, with
  path exclusions for the mirrors.
- **Breaking: job store records are snake_case.** jobs.jsonl, attempts.jsonl and
  grades.jsonl key every member in snake_case and store enum values as their Go values.
  An old grades.jsonl is refused; it has no migration.
- **Breaking for SDK callers: `apiversion` keys `api_version`.** `TypeMeta` and
  `Condition` write `api_version`, `observed_generation` and `last_transition_time`.
- **The cache key version is 8.** A manifest in the old spelling would read back empty,
  so every entry from before is a miss and runs once.
- **The disk samples store is `disk/space.jsonl`.** Its keys are `total_bytes` and
  `free_bytes`. The old `disk/samples.jsonl` is never read, and `magus doctor` notes
  that it can be deleted.
