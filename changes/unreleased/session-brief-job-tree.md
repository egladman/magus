### Added

- **`magus session --brief` prints the session's job tree.** Each job forked from this
  checkout, or under the job it acts as, shows its state, branch, head, checkout,
  dependencies, first unresolved risk and the next step. Every exited job nobody collected
  is listed with its `magus job wait` line. The tree replaces the count of other leases.
