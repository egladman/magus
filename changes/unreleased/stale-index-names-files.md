### Changed

- **A stale symbol index names the files that changed since it was built.** Each stale
  index in `magus status -o json` carries `changed` and `changed_count`, and the stale-index line under `refs`, `query` and
  `explain`, and the guard's advice name up to five modified sources and count the rest.
  Deleted files are not listed, and every rendering says so. `describe job` now grades a
  row's symbol goals against the built graph when none of those files falls inside the
  row's write paths; one that does, or an index that names no files, leaves them ungraded
  as before. Indexing stays whole-project.
