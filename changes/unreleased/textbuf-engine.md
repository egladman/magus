### Added

- **`internal/textbuf` holds a text buffer with anchors.** A copy-on-write sum tree
  carries a rope of 128-byte chunks and one tree of anchors per bias, after Zed's
  design: offset to line and column, and anchor to offset, take O(log n), and a
  snapshot takes O(1). Edits apply relative to anchors, and a formatter's output
  applies as a line diff that keeps anchors outside the changed bytes. It is the
  engine for concurrent edits to one file in a shared checkout; nothing calls it yet.
