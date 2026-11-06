### Changed

- **Breaking: a plan refuses a `ci` body that does more than `ctx.needs`**, naming
  the target and each operation, since no shard would run it. It also refuses a
  step the declared `ctx.needs` chain does not name.
