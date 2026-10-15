### Fixed

- **The model-tier lens reports an unrecorded cost and token count as unknown, not 0.** No
  host reports what a job used yet, so every job outcome carried `0` for both and the lens
  showed every tier as free. A job outcome now records `usage_tokens` and `usage_cost_usd`
  only when a host reported them, and a lens row's `mean_cost_usd` and `median_tokens` are
  `null` unless every job in the row carries the figure. The `context_tokens` and `cost_usd`
  zeros an older magus wrote into the history are not read. The console dashboard still
  shows `0` for an unknown figure until its wire fields become optional.
