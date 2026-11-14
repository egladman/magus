### Fixed

- **A target's cache key carries the keys of the targets it `ctx.needs`.** A change
  to a needed target's inputs now misses the composer, even one that narrows its key
  with `ctx.readsFiles`. Before, the agents guide's test replayed a pass after the
  hook it runs had changed. A needed project that does not resolve leaves the
  composer uncached.
