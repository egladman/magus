### Added

- **Three hack/dev scripts for handing work to a worker.** `bootstrap-worktree` builds
  `./magus` and the graph in a fresh worktree, takes a job's lease, and refuses unless the
  row names that worktree and its checkpoint. `render-brief` renders a worker's brief from
  the job row: its write and deny paths, its one check behind `./magus status --wait`, and
  `job exit` last. `check-guard-blocks` replays a fixed set of calls the guard should stop
  through the hooks every agent host's config wires (Claude Code, Codex and Cursor, each
  in its own event and reply format), runs every case for every host, and fails at the end
  when the guard let one through, which a hook that cannot find `magus` on PATH does
  silently.
