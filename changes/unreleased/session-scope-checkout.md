### Changed

- **`magus session` lists this checkout's invocations by default.** The store is shared by
  every worktree of a repository, so the bare listing used to mix them. Invocations
  recorded before the checkout was stored are still listed, and `--brief` takes its
  last-run failures from this checkout only.
