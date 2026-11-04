### Fixed

- **A job's checkout stays guarded past repeated guard timeouts.** A bound worker's
  command stays denied, and so does a subagent's line that reaches a live job's checkout
  by `cd` or by path. A checkout `job exec` took counts as one its fork made.
