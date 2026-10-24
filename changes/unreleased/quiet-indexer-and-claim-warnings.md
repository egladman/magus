### Changed

- **Symbol indexers print nothing on success, and progress redraws collapse in logs.**
  Every spell's indexer (scip-go, scip-python, scip-typescript, rust-analyzer, scip-buzz)
  runs quietly and still prints stderr on failure. A command's carriage-return redraws
  reach a non-terminal as their last frame only, and `--log-level=debug` keeps them all.
- **An unheld process group warns once per step, with the step named.** The warning says
  which `project:target` has child processes not tied to its claim, why, and the first
  child. A claim handed back while a step waits on its needs warns of nothing.
