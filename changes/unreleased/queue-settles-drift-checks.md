### Added

- **The queue acts on the base's other required checks.** Apply reads them at the head
  (the provider's optional `required_checks`): running ones wait with `WAIT_CHECKS`, red
  ones on a head carrying the base's tip kick back with `KICK_RED`, and red ones from an
  older base get one update commit merging the base in, which runs them again.
