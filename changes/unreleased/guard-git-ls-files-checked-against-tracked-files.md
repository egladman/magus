### Changed

- **`git ls-files` is checked against the tracked files.** Alone or piped into a search of
  its paths, it is refused with the query when the graph reproduces it, and the refusal
  names any tracked match the graph does not index.
