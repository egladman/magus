### Added

- **`internal/textbuf` holds a text buffer with anchors.** A copy-on-write sum tree carries
  a rope of chunks and one tree of anchors per bias, after Zed's design. Offset to line,
  and anchor to offset, take O(log n), and a snapshot takes O(1). A formatter's output
  applies as a line diff that keeps anchors outside the changed bytes.
