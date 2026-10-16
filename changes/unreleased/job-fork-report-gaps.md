### Fixed

- **`magus job fork` says when it cannot tell which files outside the write paths use
  them.** With no symbol index, the list printed nothing, which read the same as no file
  using them. The fork now says the list is unknown and names `magus graph build`.
