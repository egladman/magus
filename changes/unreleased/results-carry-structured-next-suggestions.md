### Added

- **Results carry structured `next` suggestions.** `query`, `explain`, `describe file`,
  affected listings and failing results carry up to three `{id, command, argv, why}`
  entries, filtered by the acting lease's role and journaled per session.
