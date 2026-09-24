### Added

- **`magus run` and `magus affected` take `--preflight <target>[,<target>...]`.** The named
  targets run first across every selected project; a failure stops everything, exits 3
  (MGS3020) and names the target, projects and fix. A green pass is not repeated. A name
  outside the invoked target's `ctx.needs` closure is refused, exit 2 (MGS3021). With
  `affected --plan` a red pass prints no plan.
