### Added

- **`magus diff --unread` lists the hunks no read mark covers.** Marks are keyed by hunk
  content, so an edited hunk is unread again. It always exits 0, and says the state is
  unknown when the marks cannot be read.
