### Fixed

- **Breaking for SDK callers: `magus clean` keeps outputs the VCS tracks.** It
  deleted committed generated files, with or without `--cache`, and left the tree
  dirty. `CleanOutputs` returns `CleanedOutputs`, listing removed and kept paths.
