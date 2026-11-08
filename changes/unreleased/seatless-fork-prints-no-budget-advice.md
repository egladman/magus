### Changed

- **`job fork` prints no build-budget advice for a row that holds no seat.** A
  read-only row without a check runs no target, so its text output no longer
  says the budget is full.
