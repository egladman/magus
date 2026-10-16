### Changed

- **A leased worker's rw run is held to its write paths.** worker-check-only refuses a
  `magus run` under the rw charm, typed or from `default_charms`, when a glob its target
  or that target's `ctx.needs` chain declares writing lies outside them, or the target
  declares no write. The refusal names the globs and the same run without the charm.
