### Fixed

- **`magus job fork` and `magus describe job` say when they cannot tell which files
  outside the write paths use them.** With no symbol index, the list printed nothing,
  which read the same as no file using them. Both now say the list is unknown and name
  `magus graph build`; `-o json` carries the reason as `referenced_outside_gap`.
- **Ending a job keeps its checkout and names the error when a wired harness fails to
  load.** The failure was dropped, so the tool rows that harness installed read as work
  and nothing said why the checkout stayed.
