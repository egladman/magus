### Changed

- **Breaking: units are the steps `ci` takes under the shard's charms and
  args.** `magus affected ci:gha --plan` evaluates each `ci` body as a dry run
  does, so a step behind `ctx.hasCharm("gha")` is planned only when the shard
  runs it. A target the body calls directly is listed under `unplanned`.
