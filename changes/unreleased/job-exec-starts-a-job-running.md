### Changed

- **`magus job exec` starts a declared job running.** `magus ls jobs` shows `running`
  for a job somebody took, and HOLDER names the worktree it was taken in (`-` until
  somebody takes it, `server` for magus's own jobs) instead of `session` on every row.
  `-o json` keeps `holder` as before.
