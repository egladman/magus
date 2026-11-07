### Changed

- **A tree that does not load can undo its own commits.** While no magus loads the
  tree, `git revert <commit>` and `git reset --keep <commit>` run when every commit they
  undo is one no remote holds. `git restore` and `git checkout --` put back any file the
  load error names, not only a missing import.
- **The hook glue names a linked checkout's PATH fallback by what it is.** A checkout with
  no `./magus` beside a session checkout that has one says which PATH magus judged. It no
  longer asks whether the checkout is magus's own Go module.
