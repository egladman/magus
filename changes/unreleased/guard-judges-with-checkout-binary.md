### Fixed

- **A job checkout's own ./magus judges the calls that land in it.** A shell line that `cd`s
  into another checkout, like a write into one, is handed to that checkout's ./magus when it
  was built at the checkout's HEAD or a commit after it, so a branch that extends types or
  bindings no longer locks its own checkout out of `:rw` runs. The session's policy still
  applies, and the verdict names the binary that judged. A ./magus behind HEAD, unstamped or
  missing is not asked, and the session's binary judges as before.
