### Changed

- **Breaking: shards pack against a configured machine.** `ci.machine`
  (`name`, `memory_mb`, `cores`) replaces the planning host's memory as the shard
  memory budget, and the plan records it under `machine`. A machine with one size
  set is an error; none is a stated fallback under `fallbacks`.
