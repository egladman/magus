### Fixed

- **A target reached through `ctx.needs` runs under its own sandbox declaration.** Only
  the target a run schedules got its `sandbox` grants; one it composed ran under its
  caller's policy. `ci` composing `test` left `test` without its `/proc` read. Each
  composed target now gets its own declaration, not the union with its caller's.
