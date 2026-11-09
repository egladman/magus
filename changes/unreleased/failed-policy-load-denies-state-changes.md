### Fixed

- **A guard policy that does not load denies every state change.** Before, only gated verbs were
  denied and the rest ran on built-in rules. Now it waits until a side of the policy loads.
  Reads, policy edits, VCS recovery and `./magus` rebuilds still run. Built-ins standing in for an
  unresolved side are gone; the resolve error is reported.
