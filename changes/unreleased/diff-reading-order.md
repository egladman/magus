### Added

- **`magus diff` prints the order to read the hunks in.** Definitions come before their uses,
  interfaces before implementations, tests after the code they exercise. Each hunk names the
  relationship that placed it, and a completeness line proves every hunk appears once. A
  stale or missing symbol index leaves it out with a note.
