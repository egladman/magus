### Changed

- **Breaking for SDK callers: a domain type no longer says "row".**
  `agent.StepRow` is `agent.StepSpec` and `WithStepRows` is `WithStepSpecs`.
  `types.ModelRow` is `types.HarnessModel`, `types.ToolRow` is `types.ToolStatus`,
  and `agent.ModelTierSourceRow` is `ModelTierSourceJob`, whose value is `job`.
  `types.ConformanceRow` is `types.ConformanceDistribution`. `ToolRowsVersionFile`
  and `ToolModelRowsFile` are `ToolVersionFile` and `ToolModelsFile`; the files they
  name keep their names. `Magus.ModelRows` is `Models`.
- **Breaking: the JSON, YAML and Buzz keys follow.** `rows` is `models` on the
  model-tier lens, `tools` on a harness's tool table, `distributions` on the
  conformance lens, `shapes` on the grouping lens and `jobs` on an unmerged report.
  A deferred item's `row` is `job`.
- **The `namewords` analyzer takes a `banned` option.** It keeps a word out of every
  declared name in some packages, and the root config bans `Row` and `Rows` in the
  root package and under `types/`.
- **A stored job or attempt whose enum spelling magus does not list is refused.**
  The read names the spelling and the record, where it once passed the value on
  to a Buzz conversion that panicked on it.
