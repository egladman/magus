### Fixed

- **A pipe no longer reports an upstream of unknown status when that upstream runs a
  command.** The fork a stage makes to run a command was counted as a stage that never
  left an exit status. Under `-o jsonl`, the notice for an upstream killed before leaving
  one is a `run.notice` record naming its pid.
