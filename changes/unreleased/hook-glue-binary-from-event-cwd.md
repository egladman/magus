### Fixed

- **The hook glue runs the magus of the checkout a call runs in.** It looks for `./magus`
  from the event's `cwd` first, so a subagent in its own worktree is judged by that
  tree's binary rather than the session's or an older one on PATH. `HOST_CWD_PATH`
  renames the field.
