### Fixed

- **A foreground worker is bound to the spawn its host filed it under.** Parallel foreground
  spawns no longer hand a worker the oldest waiting spawn's job. The worker's first call
  joins the spawn whose title Claude Code filed for that subagent. A host that files none
  falls back to the lease the hook process declares. A subagent no waiting spawn names
  runs unbound with a `spawn-unplaced` advisory instead of being refused.
