### Changed

- **A graph read no longer evaluates the workspace when the stored graph is current.**
  `magus query`, `explain`, `path`, `graph stats` and `graph export` answer from the
  store when its fast stamps match, and evaluate the workspace when they do not.
