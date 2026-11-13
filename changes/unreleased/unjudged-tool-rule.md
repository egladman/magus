### Added

- **The `unjudged-step` rule** names a step no row declares: a host tool call whose input no rule reads, from a tool the host's tool rows do not list, or a `Magus.Judge` step of an undeclared kind. It denies by default; `advise` in `magus\guard.builtins` notes it once per session. A tool the rows call inert passes before any rule loads.
