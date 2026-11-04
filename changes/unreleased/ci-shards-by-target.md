### Changed

- **Breaking: a shard plan places targets, not projects.** Each shard lists
  `units`, one `{project, target}` per target a project's `ci` composes, and
  shared prerequisites count once. `magus run ci --stdin --shard <id>` runs those
  units, and refuses a plan without them. Units with no history are listed under
  `unmeasured`.
- **Breaking: the plan's units are the steps `ci` takes under the shard's charms
  and args.** `magus affected ci:gha --plan -- <args>` evaluates each `ci` body as
  a dry run does, so a step behind `ctx.hasCharm("gha")` is planned only when the
  shard runs it. A target the body calls as a function is listed under
  `unplanned`. `magus run --stdin` refuses a plan evaluated under other charms or
  args.
- **Breaking: shards pack against a configured runner class.** `ci.runner`
  (`name`, `memory_mb`, `cores`) replaces the planning host's memory as the shard
  memory budget, and the plan records it under `runner`. A class with one size
  set is an error; no class is a stated fallback under `fallbacks`.
- With no recorded history the plan opens at most 2 shards, and says so under
  `fallbacks`.
- **Breaking: a shard's `label` is a digest of its units**, and the shortened
  project list moved to `name`. Each shard carries `duties`: `cache-export` and
  `notes-verify` each land on exactly one shard. `magus run --stdin` refuses a
  shard whose projects or label disagree with its units.
- The planner now errors when its partitioner drops, repeats or invents a unit,
  instead of falling back to an even split.
