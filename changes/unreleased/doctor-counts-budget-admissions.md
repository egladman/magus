### Added

- **`magus doctor` reports `budget-admissions`.** It reads `admissions.jsonl` and
  counts refusals in the last day and waits past a quarter of their bound. It
  changes nothing. `server status` also prints how a holder's memory was sized
  for each holder over a quarter of the budget.
