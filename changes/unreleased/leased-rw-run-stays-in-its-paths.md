### Changed

- **A leased worker's `:rw` run is held to its write paths.** The worker-check-only guard
  rule refuses a `magus run` whose target names the rw charm when a glob the target, or a
  target its `ctx.needs` chain reaches, declares writing lies outside the lease's write
  paths, and when a target declares no write at all. A leased `dprint:rw` had rewritten a
  file outside its lease, which the audit could only report after the write. The refusal
  names the globs and the same run without the charm.
