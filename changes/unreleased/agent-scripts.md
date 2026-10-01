### Added

- **Three hack/dev scripts for handing work to a worker.** `bootstrap-worktree` builds
  `./magus` and the graph in a fresh worktree, takes a job's lease, and refuses unless the
  row names that worktree and its checkpoint. `render-brief` renders a worker's brief from
  the job row: its write and deny paths, its one check behind `./magus status --wait`, and
  `job exit` last. `show-guard-health` replays a fixed set of tool calls through the hook
  commands `.claude/settings.json` wires and fails when the guard lets one through, which a
  hook that cannot find `magus` on PATH does silently.
