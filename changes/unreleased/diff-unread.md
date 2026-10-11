### Added

- **`magus diff --unread` narrows the review to the hunks no read mark covers.** It filters the
  viewer and the report under every `-o`; `-o name` prints one `path:start-end` per hunk. Marks
  are keyed by hunk content. It always exits 0, and says the read state is unknown when the
  marks cannot be read.
