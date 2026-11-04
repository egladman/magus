### Changed

- **Breaking: a shard plan places targets, not projects.** Each shard lists
  `units`, one `{project, target}` per target a project's `ci` composes, and
  shared prerequisites count once. `magus run ci --stdin --shard <id>` runs those
  units, and refuses a plan without them and args after `--`. Units with no
  history are listed under `unmeasured`.
