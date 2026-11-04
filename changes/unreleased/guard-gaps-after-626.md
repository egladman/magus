### Fixed

- **`magus job exit` refuses a result while the job's work is uncommitted.** It names the
  paths, and the row keeps its state so its holder commits and files again.
- **`magus job fork` no longer leaves a `./magus` built at another commit.** An adopted
  checkout with a stale binary gets one built at its base, and a relative checkout in a
  `--stdin` record is made absolute.
- **`magus affected <target> --base <ref>` keeps the ref.** Before, the next flag was read
  as the ref.
- **The guard refuses `git -C <other repo> worktree add` and ref moves an integration row
  could slip past.** A row with no standing worktree is held to the same refusals.
- **`magus\graph(languages: [...])` freshens only the indexes of the languages named.**
  lint-rules asks for go and typescript and no longer rebuilds every other index.
