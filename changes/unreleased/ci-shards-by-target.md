### Changed

- **Breaking: a shard plan places targets, not projects.** Each shard in
  `magus affected ci --plan` lists `units`, the `{project, target}` pairs it
  runs: one per target a project's `ci` composes directly. A shard costs its
  setup plus each target its units need, counted once, so one large project
  spreads over several runners. Units with no recorded duration are listed under
  `unmeasured`. `magus run ci --stdin --shard <id>` runs the shard's units and
  refuses a plan without them; print the plan again.
- **Breaking: `magus run --stdin` refuses args after `--`.** A unit is a target
  `ci` composes, and `ctx.needs` hands it none. Run a target that takes args by
  name.
