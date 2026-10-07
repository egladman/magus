### Fixed

- **MGS4008 no longer pairs a root tree glob's write with a nested project's own reads.**
  A write glob stops at a nested project as the write itself does, so root `format`
  rewriting `**/*.go` is no longer reported as racing a library's reads of its own files.
