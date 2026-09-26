### Fixed

- **A queue hook's sandbox carries the base's target grants.** It already took the grants
  of every spell the base resolved; it now adds each target's own `sandbox` declaration.
  The gate stacks a target's sandbox on the hook's, so a grant the hook lacked, like this
  repo's test target reading `/proc`, was unusable.
