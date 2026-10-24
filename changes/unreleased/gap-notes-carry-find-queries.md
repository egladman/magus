### Added

- **`magus notes capture --gap` records a harness gap you can find again.** Each
  `--anchor` (`symbol:<key>`, `file:<path>#<decl>` or `diagnostic:<code>`) must resolve
  before anything is written, and the note stores the `magus refs`, `magus explain` and
  `magus query` lines that look it up. `magus notes ls --tag gap/open` lists open gaps with a
  `find it:` block, and an anchor whose code moved shows `stale` there.
- **Notes can anchor a diagnostic code.** `diagnostic` joins the anchor kinds and resolves
  to the graph's `diagnostic:<code>` node.
