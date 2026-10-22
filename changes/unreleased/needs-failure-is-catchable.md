### Fixed

- **A `catch` around `ctx.needs` now runs when a needed target fails.** The failure
  ended the waiting target before its catch body ran, so cleanup written there was
  skipped. It is now thrown at the `ctx.needs` call. Uncaught, the target fails with the
  same error as before.
