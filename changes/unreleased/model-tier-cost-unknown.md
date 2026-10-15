### Fixed

- **The model-tier lens reports an unrecorded cost and token count as unknown, not 0.**
  Nothing filled what a job used, so every job outcome carried `0` for both and the lens
  showed every tier as free. A job outcome now records `usage_tokens` and `usage_cost_usd`
  from its holder's trail: the context every call reported, priced at the cache-read rate
  the way `magus session show` prices a session, a floor. Both stay unknown when a call
  reported no context size, billing is not metered, or the model has no row in the model
  table, and a lens row's `mean_cost_usd` and `median_tokens` are
  `null` unless every job in the row carries the figure. The `context_tokens` and `cost_usd`
  zeros an older magus wrote into the history are not read. The insight API's two fields are
  optional, absent when unknown, and the console dashboard shows `unknown` for them.
