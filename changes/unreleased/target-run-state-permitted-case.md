### Fixed

- **A target run's `permitted` state is a valid case.** Buzz's `TargetRunState` enum
  gains `permitted`, and `Valid` accepts the state the server writes when a deviation
  ledger permits a target's findings. Enum cases now come from the Go constants, so a
  hand list can no longer leave one out.
