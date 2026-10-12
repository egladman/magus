### Added

- **`job fork`, `describe job` and `job apply` name the `ci` targets nothing grades.**
  Targets of the workspace's `ci` chain that read the job's write paths, and that neither
  the check nor a goal runs, are listed with the goal JSON to add (`unrun_gates` in
  `-o json`). A note only; a fork is never refused over it.
