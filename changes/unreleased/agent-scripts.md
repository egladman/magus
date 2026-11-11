### Added

- **Three hack/dev scripts for handing work to a worker.** `bootstrap-worktree` builds
  `./magus` and the graph in a fresh worktree, takes a job's lease, and refuses unless the
  row names the worktree and checkpoint. `job-view` renders a job's spec or filed
  result as Markdown. `check-guard-blocks` replays calls the guard should stop through
  each agent host's hooks, failing on any that pass.
