### Fixed

- **A real deny stays a deny when another phase ran out of time.** `job fork` defers a
  check's grade only when the guard's deny is about the clock.
