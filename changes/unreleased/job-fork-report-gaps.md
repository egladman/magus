### Fixed

- **`magus job fork` says when it cannot tell which files outside the write paths use
  them.** With no symbol index, the list printed nothing, which read the same as no file
  using them. The fork now says the list is unknown and names `magus graph build`.
- **Ending a job keeps its checkout and names the error when a wired harness fails to
  load.** The failure was dropped, so the tool rows that harness installed read as work
  and nothing said why the checkout stayed.
