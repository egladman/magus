### Removed

- **Breaking: `magus job exec --session` and `--vacate`.** The guard now binds whoever
  runs `magus job exec`, keyed on the host's session and subagent ids, so a subagent's
  exec never binds its parent. Exec records only the base and checkout. After an exited
  or ended job, the next exec takes a new one.
