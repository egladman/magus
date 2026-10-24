### Changed

- **An integration job's `git merge` loop over literal branches is pinned by a test.** A
  `for b in x y; do git merge --no-ff --no-edit "$b"; done` passes when every word is a
  branch the row integrates, and is refused when a word is unlisted or computed.
