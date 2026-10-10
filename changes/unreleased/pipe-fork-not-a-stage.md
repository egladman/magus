### Fixed

- **A pipe no longer reports an upstream of unknown status when that upstream runs a
  command.** The fork a magus stage makes to run a target's command looks like that stage
  until it execs, and the stage downstream counted it as a stage of its own that never
  left an exit status. Under `-o jsonl` the notice for an upstream killed before it could
  leave one is now a `run.notice` record carrying `upstream_pid` and `upstream_command`,
  rather than a line of prose.
