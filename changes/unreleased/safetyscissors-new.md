### Added

- **`safetyscissors.New` opens the whole harness in one call.** The `*SafetyScissors` it
  returns holds the job store and the session writer under one directory, with the
  budgets, tier set and sandbox as options. `Command` confines a child to a lease, and
  `SpawnCeiling` refuses a child above its parent's tier. A part left out returns an
  `*UnconfiguredError` naming its option.
