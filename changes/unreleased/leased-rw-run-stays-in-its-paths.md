### Changed

- **A leased worker's rw run is held to its write paths.** worker-check-only refuses a
  `magus run` under the rw charm, typed or from `default_charms`, when a glob its target
  writes, or a file its `ctx.needs` chain modifies in the same project, lies outside them.
  A chain that declares no write passes. The refusal names the globs.
