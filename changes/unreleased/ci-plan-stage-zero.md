### Added

- **A CI plan computes shared work once, in stage zero.** When shards would each compute
  one prerequisite, `magus affected ci --plan` splits their dependents and lists it under
  `stage_zero`. `magus run --stdin --shard 0a` runs one such shard; its cache
  archive replays the work in every other shard. A plan with no shared work renders as
  before.
