### Changed

- **Breaking: Buzz files follow upstream's strict rules.** Every argument after the
  first must be labeled, top-level statements must sit in `fun main()`, and generic
  type arguments must be named. Buzz written to the old rules is refused.
