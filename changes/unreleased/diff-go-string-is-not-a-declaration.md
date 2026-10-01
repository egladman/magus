### Fixed

- **Go code inside a string or comment no longer reads as a declaration.** The golang diff
  driver took `var i = 0;` in Buzz held by a Go raw string for a Go declaration, in both
  git's change regions and magus's hunk placement. Lines inside a Go string literal or
  block comment now stay with the declaration around them.
