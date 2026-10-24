### Changed

- **A split file that names a concept is told to rename to it.** When a file-split violation
  carries a `rename`, the conformance lint prints `name the concept: <rename>` and fixes it
  with `git mv <file> <rename>`, then a fold. A file named for one function is renamed for the
  concept it holds, not split again.
