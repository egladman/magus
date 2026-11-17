### Added

- **The `libs/safetyscissors` module, the harness layer as a library.** It runs with no
  magus binary, magusfile or workspace (ADR 0050). It holds `budget`, the slot limiter,
  the machine-wide memory budget and the `Price` and `Pricer` primitives; `verdict` and
  `step`, the guard's decisions and steps; `tier`, the model tier set; `job`, the job
  record types; `session`, the session store; and `trail`, the activity event and its
  readers. JSON and JSON Lines come from `libs/codec` and secrets from `libs/secrets`.
