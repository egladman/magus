### Added

- **The `libs/safetyscissors` module, the harness layer as a library.** It runs with no
  magus binary, magusfile or workspace (ADR 0050). It holds `budget`, `verdict`, `step`,
  `tier`, `job`, `session`, `sandbox` and `trail`; JSON comes from `libs/codec` and
  secrets from `libs/secrets`.
