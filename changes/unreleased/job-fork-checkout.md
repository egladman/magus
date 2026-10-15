### Added

- **`magus job fork --checkout <dir>` makes the job its own checkout and removes it when the job ends.** It is a git worktree, an hg share or a jj workspace at the job's checkpoint, holding a copy of the forking checkout's own `./magus` when it runs one. The job is taken there and nowhere else.
