### Changed

- **Breaking: a shard plan places targets, not projects.** Each shard lists
  `units`, one `{project, target}` per target a project's `ci` composes. A shard
  costs its setup plus each target its units need, counted once, so one large
  project spreads over several runners. Units with no history are listed under
  `unmeasured`.
- **Breaking: `magus run ci --stdin --shard <id>` runs that shard's units.** It
  refuses a plan without units, and args after `--`, since `ctx.needs` hands a
  composed target none.
