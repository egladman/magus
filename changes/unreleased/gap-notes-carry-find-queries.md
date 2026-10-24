### Added

- **`magus notes capture --gap` records a harness gap you can find again.** Each anchor
  (`symbol:`, `file:<path>#<decl>` or the new `diagnostic:` kind) must resolve first, and
  the note stores the lines that look it up. `magus notes ls --tag gap/open` lists them
  with a `find it:` block and marks moved code `stale`.
