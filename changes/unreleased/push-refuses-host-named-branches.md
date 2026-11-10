### Changed

- **The guard refuses a branch named by an agent host, and `merge-base-into-branches.buzz` pushes only with `--push`.**
  A name ending in a six-digit hex id, or starting with `claude/`, is denied with the rename command. The script merges and leaves the branch unpushed unless `--push` is given.
