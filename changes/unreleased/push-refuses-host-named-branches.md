### Changed

- **The branch-name rule scores the commits a push publishes.** `git push origin <sha>:refs/heads/<name>` and `git -C <dir> push` were scored by the topics of the checkout the call ran from. They now diff the pushed source against the remote's main in the checkout that holds it.

- **The guard refuses a branch named by an agent host, and `merge-base-into-branches.buzz` pushes only with `--push`.**
  A name ending in a six-digit hex id, or starting with `claude/`, is denied with the rename command. The script merges and leaves the branch unpushed unless `--push` is given.
