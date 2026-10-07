### Fixed

- **`--root` from another checkout no longer mixes in the cwd's workspace.** `refs
  --rename` no longer panics: its guard grading inspects, loads rules from and reads the
  graph index of the workspace the command loaded. `session load` rejudges commands by
  that workspace's shell rules.
