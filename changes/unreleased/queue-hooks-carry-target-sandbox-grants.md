### Fixed

- **A queue hook's sandbox carries the base's target grants.** The queue already bounded
  each hook by the grants of every spell the base resolved; it now adds each target's
  own `sandbox` declaration too. The gate's magus stacks a target's sandbox on the
  hook's, so a grant the hook lacked, like this repo's test target reading `/proc`, was
  one no test under it could use.
